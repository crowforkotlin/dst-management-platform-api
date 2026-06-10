package server

import (
	"dst-management-platform-api/app/dashboard"
	"dst-management-platform-api/app/logs"
	"dst-management-platform-api/app/mod"
	"dst-management-platform-api/app/platform"
	"dst-management-platform-api/app/player"
	"dst-management-platform-api/app/room"
	"dst-management-platform-api/app/tools"
	"dst-management-platform-api/app/user"
	"dst-management-platform-api/database/dao"
	"dst-management-platform-api/database/db"
	"dst-management-platform-api/embedFS"
	"dst-management-platform-api/logger"
	"dst-management-platform-api/middleware"
	"dst-management-platform-api/scheduler"
	"dst-management-platform-api/utils"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	static "github.com/soulteary/gin-static"
)

func Run() {
	// 绑定启动参数
	bindFlags()

	// 打印版本
	if versionShow {
		fmt.Println(utils.Version + "\n" + runtime.Version())
		return
	}

	// 控制台命令
	if consoleCmd != "" {
		runConsole(consoleCmd, dbPath)
		return
	}

	// 初始化日志
	logger.InitLogger(logLevel)

	// macOS 自动检测 Steam DST 并创建运行时链接
	if runtime.GOOS == "darwin" {
		macOSAutoSetup()
	}

	// 平台检测
	logger.Logger.Infof("运行平台: %s/%s", runtime.GOOS, runtime.GOARCH)
	logger.Logger.Infof("DST配置目录: %s", utils.ExpandHome(utils.ClusterPath))

	// 检查DST配置目录写入权限
	clusterDir := utils.ExpandHome(utils.ClusterPath)
	if err := utils.EnsureDirExists(clusterDir); err != nil {
		logger.Logger.Errorf("无法创建DST配置目录 %s: %v", clusterDir, err)
		logger.Logger.Error("请检查目录权限，不要用sudo运行DMP，应以当前用户身份运行")
	} else {
		// 测试写入权限
		testFile := fmt.Sprintf("%s/.dmp_write_test", clusterDir)
		if err := utils.TruncAndWriteFile(testFile, "test"); err != nil {
			logger.Logger.Errorf("DST配置目录无写入权限: %v", err)
			logger.Logger.Error("请修复目录权限: chown -R $(whoami) %s", clusterDir)
		} else {
			os.Remove(testFile)
			logger.Logger.Info("DST配置目录权限检查通过")
		}
	}

	// 初始化文件
	embedFS.GenerateDefaultFile()

	// 初始化数据库
	db.InitDB(dbPath)
	userDao := dao.NewUserDAO(db.DB)
	systemDao := dao.NewSystemDAO(db.DB)
	roomDao := dao.NewRoomDAO(db.DB)
	roomSettingDao := dao.NewRoomSettingDAO(db.DB)
	worldDao := dao.NewWorldDAO(db.DB)
	globalSettingDao := dao.NewGlobalSettingDAO(db.DB)
	uidMapDao := dao.NewUidMapDAO(db.DB)

	// 开启定时任务
	scheduler.Start(roomDao, worldDao, roomSettingDao, globalSettingDao, uidMapDao)

	r := gin.New()

	// 请求日志格式
	r.Use(gin.LoggerWithConfig(gin.LoggerConfig{
		Formatter: logger.AccessFormatter,
		Output:    logger.AccessWriter,
	}))
	// panic恢复，将panic日志写入runtime.log
	r.Use(gin.CustomRecoveryWithWriter(logger.RuntimeWriter, func(c *gin.Context, recovered interface{}) {
		logger.Logger.Errorf("panic recovered: %v", recovered)
		c.AbortWithStatus(500)
	}))
	// 静态资源缓存
	r.Use(middleware.CacheControl())

	// debug日志等级下，注册pprof路由
	if logLevel == "debug" {
		logger.Logger.Debug("debug模式已开启")
		logger.Logger.Warn("debug模式会无条件暴露各种运行数据，请勿在生产环境开启debug")
		pprof.Register(r)
	} else {
		// 设置生产环境
		gin.SetMode(gin.ReleaseMode)
	}

	// 初始化即注册路由
	user.NewHandler(userDao).RegisterRoutes(r)
	room.NewHandler(userDao, roomDao, worldDao, roomSettingDao, globalSettingDao, uidMapDao).RegisterRoutes(r)
	mod.NewHandler(roomDao, worldDao, roomSettingDao, userDao).RegisterRoutes(r)
	dashboard.NewHandler(userDao, roomDao, worldDao, roomSettingDao, globalSettingDao).RegisterRoutes(r)
	platform.NewHandler(userDao, roomDao, worldDao, systemDao, globalSettingDao, uidMapDao, roomSettingDao).RegisterRoutes(r)
	logs.NewHandler(userDao, roomDao, worldDao, roomSettingDao).RegisterRoutes(r)
	tools.NewHandler(userDao, roomDao, worldDao, roomSettingDao).RegisterRoutes(r)
	player.NewHandler(userDao, roomDao, worldDao, roomSettingDao, uidMapDao, globalSettingDao).RegisterRoutes(r)

	r.Use(static.ServeEmbed("dist", embedFS.Dist))

	// 启动服务器
	var err error
	if cert != "" && key != "" {
		// 证书文件和私钥文件都不为空，则启动https
		err = r.RunTLS(fmt.Sprintf(":%d", bindPort), cert, key)
	} else {
		// 否则启动http
		err = r.Run(fmt.Sprintf(":%d", bindPort))
	}
	if err != nil {
		panic(fmt.Sprintf("启动服务器失败: %s", err.Error()))
	}
}

// macOSAutoSetup 在 macOS 上自动检测 Steam 安装的 DST 专用服务器并创建运行时链接
// 包括: dst/bin 符号链接、dst/bin64 wrapper 脚本、steamcmd wrapper、version.txt
func macOSAutoSetup() {
	// 标准 Steam DST 安装路径
	dstAppDir := filepath.Join(
		os.Getenv("HOME"),
		"Library", "Application Support", "Steam", "steamapps", "common",
		"Don't Starve Together Dedicated Server",
	)

	// 检查 DST 是否已安装
	if !utils.FileDirectoryExists(dstAppDir) {
		logger.Logger.Warn("macOS: 未检测到 Steam DST 专用服务器安装")
		logger.Logger.Warnf("macOS: 预期路径: %s", dstAppDir)
		logger.Logger.Warn("macOS: 请先通过 Steam 安装 Don't Starve Together Dedicated Server")
		logger.Logger.Warn("macOS: Steam 库 -> 工具 -> Don't Starve Together Dedicated Server")
		return
	}

	logger.Logger.Infof("macOS: 检测到 DST 安装: %s", dstAppDir)

	// 二进制实际路径 (.app/Contents/MacOS)
	binMacOSDir := filepath.Join(dstAppDir, "dontstarve_dedicated_server_nullrenderer.app", "Contents", "MacOS")
	binName := "dontstarve_dedicated_server_nullrenderer"

	// 1. 创建 dst/bin/ 符号链接 (32-bit 兼容)
	if !utils.FileDirectoryExists("dst/bin") {
		if err := os.MkdirAll("dst/bin", 0755); err != nil {
			logger.Logger.Errorf("macOS: 创建 dst/bin 失败: %v", err)
		} else {
			// 创建符号链接指向真实二进制
			symlinkTarget := filepath.Join(binMacOSDir, binName)
			symlinkPath := filepath.Join("dst", "bin", binName)
			if err := os.Symlink(symlinkTarget, symlinkPath); err != nil {
				logger.Logger.Errorf("macOS: 创建 dst/bin 符号链接失败: %v", err)
			} else {
				logger.Logger.Infof("macOS: 已创建 dst/bin/%s -> %s", binName, symlinkTarget)
			}
			// 创建 lib32 目录
			_ = os.MkdirAll("dst/bin/lib32", 0755)
		}
	}

	// 2. 创建 dst/bin64/ wrapper 脚本 (64-bit 和 luajit)
	if !utils.FileDirectoryExists("dst/bin64") {
		if err := os.MkdirAll("dst/bin64/lib64", 0755); err != nil {
			logger.Logger.Errorf("macOS: 创建 dst/bin64 失败: %v", err)
		} else {
			// wrapper 脚本: cd 到真实二进制目录并用 exec 执行
			wrapperScript := fmt.Sprintf(
				"#!/bin/bash\ncd %q && exec ./%s \"$@\"\n",
				binMacOSDir, binName,
			)
			// 64-bit 版本
			bin64Script := filepath.Join("dst", "bin64", "dontstarve_dedicated_server_nullrenderer_x64")
			if err := os.WriteFile(bin64Script, []byte(wrapperScript), 0755); err != nil {
				logger.Logger.Errorf("macOS: 创建 bin64 wrapper 失败: %v", err)
			} else {
				logger.Logger.Infof("macOS: 已创建 dst/bin64/dontstarve_dedicated_server_nullrenderer_x64 wrapper")
			}
			// luajit 版本
			luajitScript := filepath.Join("dst", "bin64", "dontstarve_dedicated_server_nullrenderer_x64_luajit")
			if err := os.WriteFile(luajitScript, []byte(wrapperScript), 0755); err != nil {
				logger.Logger.Errorf("macOS: 创建 bin64 luajit wrapper 失败: %v", err)
			} else {
				logger.Logger.Infof("macOS: 已创建 dst/bin64/dontstarve_dedicated_server_nullrenderer_x64_luajit wrapper")
			}
		}
	}

	// 3. 创建 dst/mods 符号链接
	modsSrcDir := filepath.Join(dstAppDir, "dontstarve_dedicated_server_nullrenderer.app", "Contents", "mods")
	if utils.FileDirectoryExists(modsSrcDir) && !utils.FileDirectoryExists("dst/mods") {
		if err := os.Symlink(modsSrcDir, filepath.Join("dst", "mods")); err != nil {
			logger.Logger.Errorf("macOS: 创建 dst/mods 符号链接失败: %v", err)
		} else {
			logger.Logger.Infof("macOS: 已创建 dst/mods -> %s", modsSrcDir)
		}
	}

	// 4. 创建 dst/version.txt
	if !utils.FileDirectoryExists("dst/version.txt") {
		// 尝试从 Steam ACF 文件读取版本号
		version := "0"
		acfPath := filepath.Join(
			os.Getenv("HOME"),
			"Library", "Application Support", "Steam", "steamapps", "appmanifest_343050.acf",
		)
		if utils.FileDirectoryExists(acfPath) {
			if parser, err := utils.NewParser(acfPath); err == nil {
				if buildID, ok := parser.Root.List["buildid"]; ok {
					// buildid 存在，尝试转为数字
					if _, err := strconv.Atoi(buildID); err == nil {
						version = buildID
					}
				}
			}
		}
		if err := utils.TruncAndWriteFile("dst/version.txt", version); err != nil {
			logger.Logger.Errorf("macOS: 创建 dst/version.txt 失败: %v", err)
		} else {
			logger.Logger.Infof("macOS: 已创建 dst/version.txt (版本: %s)", version)
		}
	}

	// 5. 检测 steamcmd 并创建 wrapper
	if !utils.FileDirectoryExists("steamcmd/steamcmd.sh") {
		steamcmdPath := findSteamCMD()
		if steamcmdPath == "" {
			logger.Logger.Warn("macOS: 未检测到 steamcmd，自动更新功能将不可用")
			logger.Logger.Warn("macOS: 请安装 steamcmd: brew install steamcmd")
		} else {
			_ = os.MkdirAll("steamcmd/linux32", 0755)
			_ = os.MkdirAll("steamcmd/linux64", 0755)

			// 创建 placeholder steamclient.so
			for _, sub := range []string{"linux32", "linux64"} {
				soPath := filepath.Join("steamcmd", sub, "steamclient.so")
				if !utils.FileDirectoryExists(soPath) {
					_ = os.WriteFile(soPath, []byte("# macOS placeholder\n"), 0644)
				}
			}

			// 创建 steamcmd.sh wrapper
			wrapper := fmt.Sprintf("#!/bin/bash\nexec %q \"$@\"\n", steamcmdPath)
			if err := os.WriteFile("steamcmd/steamcmd.sh", []byte(wrapper), 0755); err != nil {
				logger.Logger.Errorf("macOS: 创建 steamcmd wrapper 失败: %v", err)
			} else {
				logger.Logger.Infof("macOS: 已创建 steamcmd/steamcmd.sh -> %s", steamcmdPath)
			}
		}
	}

	logger.Logger.Info("macOS: DST 运行时环境检测完成")
}

// findSteamCMD 在常见路径中查找 steamcmd
func findSteamCMD() string {
	candidates := []string{
		"/opt/homebrew/bin/steamcmd",
		"/usr/local/bin/steamcmd",
	}

	// 也尝试 which
	if path, err := exec.LookPath("steamcmd"); err == nil {
		return path
	}

	for _, p := range candidates {
		if utils.FileDirectoryExists(p) {
			return p
		}
	}

	return ""
}
