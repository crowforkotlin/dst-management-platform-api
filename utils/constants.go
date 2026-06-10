package utils

import "runtime"

const Version = "v3.1.5"

const ApiVersion = "v3"

const HttpTimeout = 30

const JwtExpirationHours = 24

const GameModSettingPath = "dst/mods/dedicated_server_mods_setup.lua"

const DSTLocalVersionPath = "dst/version.txt"

const DSTServerVersionApi = "https://forums.kleientertainment.com/game-updates/dst"

const InternetIPApi1 = "http://ip-api.com/json/?lang=zh-CN"

const InternetIPApi2 = "http://cip.cc"

const SteamApiModDetail = "http://api.steampowered.com/IPublishedFileService/GetDetails/v1/"

const SteamApiModSearch = "http://api.steampowered.com/IPublishedFileService/QueryFiles/v1/"

// ClusterPath 根据操作系统自动设置DST配置目录
// macOS: ~/Documents/Klei/DoNotStarveTogether
// Linux: ~/.klei/DoNotStarveTogether
var ClusterPath = func() string {
	if runtime.GOOS == "darwin" {
		return "~/Documents/Klei/DoNotStarveTogether"
	}
	return "~/.klei/DoNotStarveTogether"
}()

const DmpFiles = "dmp_files"
