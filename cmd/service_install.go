package cmd

import (
	"fmt"
	"os"
	"os/exec"

	"github.com/apex/log"
	"github.com/spf13/cobra"
)

var (
	serviceFile    = "/etc/systemd/system/wings.service"
	serviceContent = `[Unit]
Description=Pterodactyl Wings Daemon
After=docker.service
Requires=docker.service
PartOf=docker.service
	
[Service]
User=root
WorkingDirectory=/etc/pterodactyl
LimitNOFILE=4096
PIDFile=/var/run/wings/daemon.pid
ExecStart=/usr/local/bin/wings
Restart=on-failure
StartLimitInterval=180
StartLimitBurst=30
RestartSec=5s
	
[Install]
WantedBy=multi-user.target`
	serviceCmd = &cobra.Command{
		Use:   "service-install",
		Short: "Use to install wings.service automatically",
		Run:   installService,
	}
)

func installService(cmd *cobra.Command, args []string) {
	if _, err := os.Stat(serviceFile); err == nil {
		log.WithField("error", "service file exists").Fatal("service aready installed")
		return
	}

	f, cf_err := os.Create(serviceFile)

	if cf_err != nil {
		log.WithField("error", cf_err).Fatal("error while creating service file")
		return
	}

	content := []byte(serviceContent)

	_, wf_err := f.Write(content)

	if wf_err != nil {
		log.WithField("error", wf_err).Fatal("error while write service file")
		return
	}

	enable_command := exec.Command("systemctl", "enable", "--now", serviceFile)
	cmd_enable_err := enable_command.Start()

	if cmd_enable_err != nil {
		log.WithField("error", cmd_enable_err).Fatal("error while enabling service")
		return
	}

	daemon_reload_command := exec.Command("systemctl", "daemon-reload")
	cmd_reload_err := daemon_reload_command.Start()

	if cmd_reload_err != nil {
		log.WithField("error", cmd_reload_err).Fatal("error while reloading daemon")
		return
	}

	fmt.Println("service created success!")
}
