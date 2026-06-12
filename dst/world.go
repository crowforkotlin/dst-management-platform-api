package dst

import (
	"bufio"
	"dst-management-platform-api/database/models"
	"dst-management-platform-api/logger"
	"dst-management-platform-api/utils"
	"fmt"
	"io"
	"os"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/shirou/gopsutil/v3/process"
)

type worldSaveData struct {
	worldPath             string
	serverIniPath         string
	savePath              string
	sessionPath           string
	levelDataOverridePath string
	modOverridesPath      string
	startCmd              string
	screenName            string
	models.World
}

func (g *Game) createWorlds() error {
	g.worldMutex.Lock()
	defer g.worldMutex.Unlock()

	var (
		err        error
		worldsName []string
	)

	// 保存文件
	for _, world := range g.worldSaveData {

		err = utils.EnsureDirExists(world.worldPath)
		if err != nil {
			return err
		}

		err = utils.TruncAndWriteFile(world.serverIniPath, getServerIni(&world.World))
		if err != nil {
			return err
		}

		levelData := world.LevelData
		if levelData == "" {
			levelData = "-- default level data\nreturn {}\n"
		}
		err = utils.TruncAndWriteFile(world.levelDataOverridePath, levelData)
		if err != nil {
			return err
		}

		var modData string
		if g.room.ModInOne {
			modData = g.room.ModData
		} else {
			modData = world.ModData
		}
		if modData == "" {
			modData = "return {}\n"
		}
		err = utils.TruncAndWriteFile(world.modOverridesPath, modData)
		if err != nil {
			return err
		}

		worldsName = append(worldsName, world.WorldName)
	}

	// 清理删除的世界
	fileSystemWorlds, err := utils.GetDirs(g.clusterPath, false)
	if err != nil {
		logger.Logger.Warnf("获取世界目录列表失败: %v", err)
		return nil
	}
	for _, fileSystemWorld := range fileSystemWorlds {
		if !utils.Contains(worldsName, fileSystemWorld) {
			// 清理文件
			err = utils.RemoveDir(fmt.Sprintf("%s/%s", g.clusterPath, fileSystemWorld))
			if err != nil {
				logger.Logger.Warnf("清理世界失败，删除文件失败: %v", err)
			}
			// 清理screen
			cmd := fmt.Sprintf("screen -X -S DMP_Cluster_%d_%s quit", g.room.ID, fileSystemWorld)
			err = utils.BashCMD(cmd)
			if err != nil {
				logger.Logger.Warnf("清理世界失败，清理SCREEN失败: %v", err)
			}
		}
	}

	return nil
}

func (g *Game) worldUpStatus(id int) bool {
	world, err := g.getWorldByID(id)
	if err != nil {
		return false
	}

	if runtime.GOOS == "darwin" {
		return utils.DstIsRunningMacOS(g.clusterName, world.WorldName)
	}

	cmd := fmt.Sprintf("ps -ef | grep %s | grep -v grep", world.screenName)
	err = utils.BashCMD(cmd)
	return err == nil
}

type PerformanceStatus struct {
	CPU     float64 `json:"cpu"`
	Mem     float64 `json:"mem"`
	MemSize float64 `json:"memSize"`
	Disk    int64   `json:"disk"`
}

func (g *Game) worldPerformanceStatus(id int) PerformanceStatus {
	var performanceStatus PerformanceStatus

	world, err := g.getWorldByID(id)
	if err != nil {
		return performanceStatus
	}

	diskUsed, err := utils.GetDirSize(world.worldPath)
	if err != nil {
		logger.Logger.Warnf("获取世界磁盘使用量失败: %v, 世界id: %d", err, world.ID)
		diskUsed = 0
	}

	performanceStatus.Disk = diskUsed

	if !g.worldUpStatus(id) {
		return performanceStatus
	}

	cmd := fmt.Sprintf("ps -ef | grep dontstarve_dedicated_server_nullrenderer | grep Cluster_%d | grep %s | grep -v luajit | grep -vi screen | awk '{print $2}'", g.room.ID, world.WorldName)
	logger.Logger.Debug(cmd)
	out, _, _ := utils.BashCMDOutput(cmd)
	logger.Logger.Debug(out)

	if len(out) < 2 {
		logger.Logger.Warnf("获取世界PID失败, 世界id: %d", world.ID)
		return performanceStatus
	}

	// macOS上可能匹配到多个进程（wrapper脚本+实际二进制），取第一个PID
	pidStr := strings.TrimSpace(strings.Split(out, "\n")[0])
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		logger.Logger.Warnf("获取世界PID失败, id: %d, err: %v", world.ID, err)
		return performanceStatus
	}

	p, err := process.NewProcess(int32(pid))
	if err != nil {
		logger.Logger.Warnf("获取世界进程失败, world: %v, err: %v", world.ID, err)
		return performanceStatus
	}

	cpu, err := p.Percent(time.Millisecond * 100)
	if err != nil {
		// macOS 上 gopsutil 可能因权限不足失败（operation not permitted），使用 ps 命令后备
		logger.Logger.Debugf("gopsutil获取CPU失败, 尝试ps后备, world: %v, err: %v", world.ID, err)
		cpuCmd := fmt.Sprintf("ps -p %d -o %%cpu= | tr -d ' '", pid)
		cpuOut, _, cpuErr := utils.BashCMDOutput(cpuCmd)
		if cpuErr == nil && strings.TrimSpace(cpuOut) != "" {
			if cpuVal, parseErr := strconv.ParseFloat(strings.TrimSpace(cpuOut), 64); parseErr == nil {
				performanceStatus.CPU = cpuVal
			}
		}
	} else {
		performanceStatus.CPU = cpu
	}

	mem, err := p.MemoryPercent()
	if err != nil {
		logger.Logger.Debugf("gopsutil获取内存使用率失败, 尝试ps后备, world: %v, err: %v", world.ID, err)
		// ps 后备获取内存
		memCmd := fmt.Sprintf("ps -p %d -o %%mem= | tr -d ' '", pid)
		memOut, _, memErr := utils.BashCMDOutput(memCmd)
		if memErr == nil && strings.TrimSpace(memOut) != "" {
			if memVal, parseErr := strconv.ParseFloat(strings.TrimSpace(memOut), 64); parseErr == nil {
				performanceStatus.Mem = memVal
			}
		}
	} else {
		performanceStatus.Mem = float64(mem)
	}

	memSize, err := p.MemoryInfo()
	if err != nil {
		logger.Logger.Debugf("gopsutil获取内存信息失败, 尝试ps后备, world: %v, err: %v", world.ID, err)
		// ps 后备获取 RSS（KB）
		rssCmd := fmt.Sprintf("ps -p %d -o rss= | tr -d ' '", pid)
		rssOut, _, rssErr := utils.BashCMDOutput(rssCmd)
		if rssErr == nil && strings.TrimSpace(rssOut) != "" {
			if rssVal, parseErr := strconv.ParseFloat(strings.TrimSpace(rssOut), 64); parseErr == nil {
				performanceStatus.MemSize = rssVal / 1024 // KB -> MB
			}
		}
	} else {
		performanceStatus.MemSize = float64(memSize.RSS / 1024 / 1024)
	}

	logger.Logger.Debug(utils.StructToFlatString(performanceStatus))

	return performanceStatus
}

func (g *Game) startWorld(id int) error {
	if runtime.GOOS != "darwin" {
		_ = utils.BashCMD("screen -wipe")
	}

	// 启动游戏后，删除mod临时下载目录
	g.acfMutex.Lock()
	defer g.acfMutex.Unlock()
	defer func() {
		err := utils.RemoveDir(fmt.Sprintf("%s/mods/ugc/%s", utils.DmpFiles, g.clusterName))
		if err != nil {
			logger.Logger.Warnf("删除临时模组失败, err: %v", err)
		}
	}()

	// 给klei擦钩子，检查so文件
	if !utils.CompareFileSHA256("dst/bin/lib32/steamclient.so", "steamcmd/linux32/steamclient.so") {
		logger.Logger.Debug("发现so文件异常，开始替换")
		replaceDSTSOFile()
	}

	var (
		err   error
		world *worldSaveData
	)

	world, err = g.getWorldByID(id)
	if err != nil {
		return err
	}

	// 如果正在运行，则跳过
	if g.worldUpStatus(id) {
		logger.Logger.Infof("当前世界正在运行中，跳过，世界ID：%d", id)
		return nil
	}

	// macOS: 启动前彻底清理该世界的所有残留进程
	if runtime.GOOS == "darwin" {
		cleanupCMD := buildCleanupCmd(g.clusterName, world.WorldName)
		_ = utils.BashCMD(cleanupCMD)
		time.Sleep(500 * time.Millisecond) // 等待进程完全退出
		// 二次清理确保无残留
		_ = utils.BashCMD(cleanupCMD)
	} else {
		cleanupCMD := buildCleanupCmd(g.clusterName, world.WorldName)
		_ = utils.BashCMD(cleanupCMD)
	}

	err = g.dsModsSetup()
	if err != nil {
		return err
	}

	logger.Logger.Debug(world.startCmd)
	err = utils.BashCMD(world.startCmd)

	return err
}

func (g *Game) startAllWorld() error {
	if runtime.GOOS != "darwin" {
		_ = utils.BashCMD("screen -wipe")
	}

	var err error

	// 给klei擦钩子，检查so文件
	if !utils.CompareFileSHA256("dst/bin/lib32/steamclient.so", "steamcmd/linux32/steamclient.so") {
		logger.Logger.Debug("发现so文件异常，开始替换")
		replaceDSTSOFile()
	}

	err = g.dsModsSetup()
	if err != nil {
		return err
	}

	for _, world := range g.worldSaveData {
		// 如果正在运行，则跳过
		if g.worldUpStatus(world.ID) {
			logger.Logger.Infof("当前世界正在运行中，跳过，世界ID：%d", world.ID)
			continue
		}

		// 启动前清理可能残留的孤儿DST进程
		// macOS上ps输出的进程命令不含screenName，需要用cluster+shard参数匹配
		cleanupCMD := buildCleanupCmd(g.clusterName, world.WorldName)
		_ = utils.BashCMD(cleanupCMD)

		logger.Logger.Debug(world.startCmd)
		err = utils.BashCMD(world.startCmd)
		if err != nil {
			return err
		}
	}

	return nil
}

func (g *Game) stopWorld(id int) error {
	world, err := g.getWorldByID(id)
	if err != nil {
		return err
	}

	// macOS: 通过 FIFO 发送 shutdown 命令，然后杀死进程
	if runtime.GOOS == "darwin" {
		_ = utils.DstSendCmdMacOS("c_shutdown()", g.clusterName, world.WorldName)
		time.Sleep(1 * time.Second)
		return utils.DstStopWorld(g.clusterName, world.WorldName)
	}

	// Linux: 通过 screen 发送 shutdown 命令
	err = utils.ScreenCMD("c_shutdown()", world.screenName)
	if err != nil {
		logger.Logger.Infof("执行ScreenCMD失败，可能是未运行: %v, cmd: c_shutdown()", err)
	}

	time.Sleep(1 * time.Second)

	// 关闭screen会话
	killCMD := fmt.Sprintf("screen -S %s -X quit", world.screenName)
	_ = utils.BashCMD(killCMD)

	// 查找并杀死DST进程
	findAndKillCMD := buildCleanupCmd(g.clusterName, world.WorldName)
	_ = utils.BashCMD(findAndKillCMD)

	return nil
}

// buildCleanupCmd 构建杀死DST孤儿进程的命令
// macOS上ps输出的进程命令不含screenName（如 DMP_Cluster_4_Master），
// 而是显示 ./dontstarve_dedicated_server_nullrenderer -console -cluster Cluster_4 -shard Master
// 因此需要用cluster+shard参数来精确匹配
func buildCleanupCmd(clusterName, worldName string) string {
	return fmt.Sprintf(
		"ps -ef | grep dontstarve_dedicated_server_nullrenderer | grep '%s' | grep '%s' | grep -v grep | grep -v screen | awk '{print $2}' | xargs kill -9 2>/dev/null",
		clusterName, worldName,
	)
}

func (g *Game) stopAllWorld() error {
	for _, world := range g.worldSaveData {
		err := g.stopWorld(world.ID)
		if err != nil {
			return err
		}
	}

	return nil
}

func (g *Game) deleteWorld(id int) error {
	_ = g.stopWorld(id)
	world, err := g.getWorldByID(id)
	if err != nil {
		return err
	}
	return utils.RemoveDir(world.savePath)
}

func (g *Game) consoleCmd(cmd string, id int) error {
	world, err := g.getWorldByID(id)
	if err != nil {
		return err
	}
	s := strings.ReplaceAll(cmd, "\"", "'")

	return utils.DstSendCmd(world.screenName, s, g.clusterName, world.WorldName)
}

func (g *Game) getWorldByID(id int) (*worldSaveData, error) {
	for i := range g.worldSaveData {
		if g.worldSaveData[i].ID == id {
			return &g.worldSaveData[i], nil
		}
	}

	return nil, fmt.Errorf("世界不存在: %d", id)
}

func getServerIni(world *models.World) string {
	contents := `[NETWORK]
server_port = ` + strconv.Itoa(world.ServerPort) + `

[SHARD]
id = ` + strconv.Itoa(world.GameID) + `
is_master = ` + strconv.FormatBool(world.IsMaster) + `
name = ` + world.WorldName + `

[STEAM]
master_server_port = ` + strconv.Itoa(world.MasterServerPort) + `
authentication_port = ` + strconv.Itoa(world.AuthenticationPort) + `

[ACCOUNT]
encode_user_path = ` + strconv.FormatBool(world.EncodeUserPath)
	return contents
}

func (g *Game) getOnlinePlayerList(id int) ([]string, error) {
	world, err := g.getWorldByID(id)
	if err != nil {
		return []string{}, err
	}

	if runtime.GOOS == "darwin" {
		// macOS: 通过 FIFO 发送命令
		cmd := `for i, v in ipairs(TheNet:GetClientTable()) do  print(string.format("playerlist %s [%d] %s <-@dmp@-> %s <-@dmp@-> %s", 99999999, i-1, v.userid, v.name, v.prefab )) end`
		err = utils.DstSendCmdMacOS(cmd, g.clusterName, world.WorldName)
	} else {
		// Linux: 通过 screen stuff 发送
		listScreenCmd := fmt.Sprintf("screen -S \"%s\" -p 0 -X stuff \"for i, v in ipairs(TheNet:GetClientTable()) do  print(string.format(\\\"playerlist %%s [%%d] %%s <-@dmp@-> %%s <-@dmp@-> %%s\\\", 99999999, i-1, v.userid, v.name, v.prefab )) end$(printf \\\\r)\"\n", world.screenName)
		err = utils.BashCMD(listScreenCmd)
	}
	if err != nil {
		return []string{}, err
	}

	// 等待命令执行完毕
	time.Sleep(time.Second * 2)

	// 获取日志文件中的list
	logPath := fmt.Sprintf("%s/server_log.txt", world.worldPath)

	// 使用反向读取，只读取最后几KB
	return readPlayerListFromEnd(logPath)
}

var (
	playerListPattern        = regexp.MustCompile(`playerlist 99999999 \[[0-9]+\] (KU_.+) <-@dmp@-> (.*) <-@dmp@-> (.+)?`)
	playerDetailPattern      = regexp.MustCompile(`playerdetail 99999999 (KU_.+) <-@dmp@-> (.*) <-@dmp@-> (\w+) <-@dmp@-> (\d+)`)
	hostPattern              = regexp.MustCompile(`\[Host]`)
)

// OnlinePlayerDetail 在线玩家详情（含 entity ID，用于快捷指令选择目标）
type OnlinePlayerDetail struct {
	UID      string `json:"uid"`
	Name     string `json:"name"`
	Prefab   string `json:"prefab"`
	EntityID int    `json:"entityID"`
}

// getOnlinePlayerDetail 获取在线玩家详情（含 entity ID）
func (g *Game) getOnlinePlayerDetail(id int) ([]OnlinePlayerDetail, error) {
	world, err := g.getWorldByID(id)
	if err != nil {
		return []OnlinePlayerDetail{}, err
	}

	// Lua 命令：获取 uid, name, prefab, entity GUID
	luaCmd := `for i, v in ipairs(TheNet:GetClientTable()) do local p = v.userid and UserToPlayer(v.userid) print(string.format("playerdetail %s %s <-@dmp@-> %s <-@dmp@-> %s <-@dmp@-> %d", 99999999, v.userid, v.name, v.prefab, p and p.GUID or 0)) end`

	if runtime.GOOS == "darwin" {
		err = utils.DstSendCmdMacOS(luaCmd, g.clusterName, world.WorldName)
	} else {
		listScreenCmd := fmt.Sprintf("screen -S \"%s\" -p 0 -X stuff \"%s$(printf \\\\r)\"\n", world.screenName, strings.ReplaceAll(luaCmd, `"`, `\\\"`))
		err = utils.BashCMD(listScreenCmd)
	}
	if err != nil {
		return []OnlinePlayerDetail{}, err
	}

	time.Sleep(time.Second * 2)

	logPath := fmt.Sprintf("%s/server_log.txt", world.worldPath)
	return readPlayerDetailFromEnd(logPath)
}

func readPlayerDetailFromEnd(logPath string) ([]OnlinePlayerDetail, error) {
	const bufferSize = 1024 * 4
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}

	startPos := fileInfo.Size() - bufferSize
	if startPos < 0 {
		startPos = 0
	}

	_, err = file.Seek(startPos, 0)
	if err != nil {
		return nil, err
	}

	buffer := make([]byte, bufferSize)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return nil, err
	}

	lines := strings.Split(string(buffer[:n]), "\n")

	var linesAfterKeyword []string
	keyword := "playerdetail 99999999"
	var found bool

	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		linesAfterKeyword = append(linesAfterKeyword, line)
		if strings.Contains(line, keyword) {
			found = true
			break
		}
	}

	if !found {
		return nil, fmt.Errorf("playerdetail not found")
	}

	var players []OnlinePlayerDetail
	seen := map[string]bool{}

	for _, line := range linesAfterKeyword {
		if matches := playerDetailPattern.FindStringSubmatch(line); matches != nil {
			if hostPattern.MatchString(line) {
				continue
			}
			uid := strings.TrimSpace(matches[1])
			if seen[uid] {
				continue
			}
			seen[uid] = true

			entityID, _ := strconv.Atoi(matches[4])
			players = append(players, OnlinePlayerDetail{
				UID:      uid,
				Name:     strings.TrimSpace(matches[2]),
				Prefab:   strings.TrimSpace(matches[3]),
				EntityID: entityID,
			})
		}
	}

	return players, nil
}

func readPlayerListFromEnd(logPath string) ([]string, error) {
	const bufferSize = 1024 * 4 // 4KB buffer

	// 打开文件
	file, err := os.Open(logPath)
	if err != nil {
		return nil, err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			logger.Logger.Errorf("文件关闭失败, err: %v", err)
		}
	}(file)

	// 获取文件大小
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, err
	}
	fileSize := fileInfo.Size()

	// 计算从哪里开始读取
	startPos := fileSize - bufferSize
	if startPos < 0 {
		startPos = 0
	}

	// 移动到起始位置
	_, err = file.Seek(startPos, 0)
	if err != nil {
		return nil, err
	}

	// 读取缓冲区内容
	buffer := make([]byte, bufferSize)
	n, err := file.Read(buffer)
	if err != nil && err != io.EOF {
		return nil, err
	}

	content := string(buffer[:n])

	// 分割成行
	lines := strings.Split(content, "\n")

	// 从后往前查找
	var linesAfterKeyword []string
	keyword := "playerlist 99999999 [0]"
	var foundKeyword bool

	// 从末尾开始遍历
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		linesAfterKeyword = append(linesAfterKeyword, line)

		if strings.Contains(line, keyword) {
			foundKeyword = true
			break
		}
	}

	if !foundKeyword {
		return nil, fmt.Errorf("keyword not found in the file")
	}

	var players []string

	// 查找匹配的行并提取所需字段
	for _, line := range linesAfterKeyword {
		if matches := playerListPattern.FindStringSubmatch(line); matches != nil {
			// 检查是否包含 [Host]
			if !hostPattern.MatchString(line) {
				uid := strings.ReplaceAll(matches[1], "\t", "")
				nickName := strings.ReplaceAll(matches[2], "\t", "")
				prefab := strings.ReplaceAll(matches[3], "\t", "")
				player := uid + "<-@dmp@->" + nickName + "<-@dmp@->" + prefab
				players = append(players, player)
			}
		}
	}

	players = uniqueSliceKeepOrderString(players)

	return players, nil
}

func (g *Game) getLastAliveTime(id int) (string, error) {
	world, err := g.getWorldByID(id)
	if err != nil {
		return "", err
	}

	_ = utils.DstSendCmd(world.screenName, "print('DMP Keepalive')", g.clusterName, world.WorldName)
	time.Sleep(1 * time.Second)

	return getWorldLastTime(fmt.Sprintf("%s/server_log.txt", world.worldPath))
}

func getWorldLastTime(logfile string) (string, error) {
	// 获取日志文件中的list
	file, err := os.Open(logfile)
	if err != nil {
		logger.Logger.Errorf("打开文件失败, err: %v, file: %v", err, logfile)
		return "", err
	}
	defer func(file *os.File) {
		err := file.Close()
		if err != nil {
			logger.Logger.Errorf("关闭文件失败, err: %v, file: %v", err, logfile)
		}
	}(file)

	// 逐行读取文件
	scanner := bufio.NewScanner(file)
	var lines []string
	timeRegex := regexp.MustCompile(`^\[\d{2}:\d{2}:\d{2}]`)

	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	if err := scanner.Err(); err != nil {
		logger.Logger.Errorf("文件scan失败, err: %v", err)
		return "", err
	}
	// 反向遍历行
	for i := len(lines) - 1; i >= 0; i-- {
		line := lines[i]
		// 将行添加到结果切片
		match := timeRegex.FindString(line)
		if match != "" {
			// 去掉方括号
			lastTime := strings.Trim(match, "[]")
			return lastTime, nil
		}
	}

	return "", fmt.Errorf("没有找到日志时间戳")
}
