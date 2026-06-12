#!/bin/bash
# DMP 饥荒管理平台 - macOS 启动/停止脚本
# 用法:
#   ./run.sh start    启动 DMP（后台运行）
#   ./run.sh stop     停止 DMP 及所有 DST 进程
#   ./run.sh restart  重启
#   ./run.sh status   查看运行状态

set -e

APP_NAME="dmp-api"
BIND_PORT="${DMP_PORT:-8082}"
PID_FILE=".dmp.pid"
LOG_FILE="logs/dmp.log"

# 颜色
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

build() {
    echo -e "${YELLOW}编译中...${NC}"
    go build -o "$APP_NAME" .
    echo -e "${GREEN}编译完成${NC}"
}

kill_all_dst() {
    # 杀死所有 DST 专用服务器进程
    pkill -9 -f dontstarve_dedicated_server_nullrenderer 2>/dev/null || true
    # 杀死所有 tail -f FIFO 进程
    pkill -9 -f "tail -f.*dst_pipes" 2>/dev/null || true
    # 清理残留的 osascript
    pkill -9 -f "osascript.*dontstarve" 2>/dev/null || true
    # 清理 screen 会话
    screen -ls 2>/dev/null | grep DMP_ | awk '{print $1}' | xargs -I{} screen -X -S {} quit 2>/dev/null || true
    # 清理 FIFO 文件
    rm -f dmp_files/dst_pipes/*.fifo dmp_files/dst_pipes/*.pid 2>/dev/null || true
}

start() {
    # 检查是否已在运行
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        echo -e "${RED}DMP 已在运行 (PID: $(cat $PID_FILE))${NC}"
        echo "使用 ./run.sh stop 先停止"
        exit 1
    fi

    # 清理残留进程
    kill_all_dst

    # 编译
    build

    # 确保日志目录存在
    mkdir -p logs

    # 后台启动
    nohup "./$APP_NAME" -bind "$BIND_PORT" >> "$LOG_FILE" 2>&1 &
    echo $! > "$PID_FILE"

    sleep 1

    # 验证启动
    if kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        echo -e "${GREEN}DMP 已启动${NC}"
        echo -e "  PID:  $(cat "$PID_FILE")"
        echo -e "  端口: $BIND_PORT"
        echo -e "  日志: $LOG_FILE"
        echo -e "  访问: http://localhost:$BIND_PORT"
    else
        echo -e "${RED}启动失败，查看日志: $LOG_FILE${NC}"
        rm -f "$PID_FILE"
        exit 1
    fi
}

stop() {
    echo -e "${YELLOW}停止 DMP...${NC}"

    # 停止 DMP 主进程
    if [ -f "$PID_FILE" ]; then
        PID=$(cat "$PID_FILE")
        if kill -0 "$PID" 2>/dev/null; then
            kill "$PID" 2>/dev/null
            sleep 1
            # 如果还没死，强制杀
            kill -9 "$PID" 2>/dev/null || true
            echo -e "  DMP 进程 (PID: $PID) 已停止"
        fi
        rm -f "$PID_FILE"
    else
        echo "  未找到 PID 文件"
    fi

    # 停止所有 DST 进程
    echo -e "${YELLOW}停止所有 DST 进程...${NC}"
    kill_all_dst

    sleep 1

    # 验证
    REMAINING=$(ps aux | grep -i dontstarve | grep -v grep | wc -l | tr -d ' ')
    if [ "$REMAINING" -eq 0 ]; then
        echo -e "${GREEN}所有进程已清理${NC}"
    else
        echo -e "${RED}仍有 $REMAINING 个残留进程${NC}"
        ps aux | grep -i dontstarve | grep -v grep
    fi
}

status() {
    echo "=== DMP 状态 ==="

    # DMP 进程
    if [ -f "$PID_FILE" ] && kill -0 "$(cat "$PID_FILE")" 2>/dev/null; then
        echo -e "DMP: ${GREEN}运行中${NC} (PID: $(cat "$PID_FILE"))"
    else
        echo -e "DMP: ${RED}未运行${NC}"
        [ -f "$PID_FILE" ] && rm -f "$PID_FILE"
    fi

    # DST 进程
    DST_COUNT=$(ps aux | grep dontstarve_dedicated_server_nullrenderer | grep -v grep | wc -l | tr -d ' ')
    if [ "$DST_COUNT" -gt 0 ]; then
        echo -e "DST 世界: ${GREEN}$DST_COUNT 个运行中${NC}"
        ps -eo pid,etime,args | grep dontstarve_dedicated_server_nullrenderer | grep -v grep | while read -r line; do
            echo "  $line"
        done
    else
        echo -e "DST 世界: ${RED}无${NC}"
    fi

    # FIFO 管道
    FIFO_COUNT=$(ls dmp_files/dst_pipes/*.fifo 2>/dev/null | wc -l | tr -d ' ')
    echo "FIFO 管道: $FIFO_COUNT 个"
}

case "${1}" in
    start)
        start
        ;;
    stop)
        stop
        ;;
    restart)
        stop
        sleep 1
        start
        ;;
    status)
        status
        ;;
    *)
        echo "用法: $0 {start|stop|restart|status}"
        exit 1
        ;;
esac
