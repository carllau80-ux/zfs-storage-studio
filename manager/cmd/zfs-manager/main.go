package main

import (
	"flag"
	"fmt"
	"os"

	"zfsmgr/internal/app"
)

func main() {
	cfgPath := flag.String("config", "/etc/zfs-platform/manager.toml", "Manager 配置文件路径(TOML)")
	flag.Parse()
	a := app.New(nil, nil)
	if err := a.Run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "启动失败:", err)
		os.Exit(1)
	}
}
