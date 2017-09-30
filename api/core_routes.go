package api

import (
	"net/http"

	"github.com/Pterodactyl/wings/constants"
	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/cpu"
	"github.com/shirou/gopsutil/host"
	"github.com/shirou/gopsutil/mem"
	log "github.com/sirupsen/logrus"
)

func GetIndex(c *gin.Context) {
	auth := GetContextAuthManager(c)

	if auth != nil && auth.HasPermission("c:info") {
		hostInfo, err := host.Info()
		if err != nil {
			log.WithError(err).Error("Failed to retrieve host information.")
		}
		cpuInfo, err := cpu.Info()
		if err != nil {
			log.WithError(err).Error("Failed to retrieve CPU information.")
		}
		memInfo, err := mem.VirtualMemory()
		if err != nil {
			log.WithError(err).Error("Failed to retrieve memory information.")
		}

		info := struct {
			Name    string `json:"name"`
			Version string `json:"version"`
			System  struct {
				SystemType string `json:"type"`
				Platform   string `json:"platform"`
				Release    string `json:"release"`
				Cpus       int32  `json:"cpus"`
				Freemem    uint64 `json:"freemem"`
			} `json:"os"`
		}{
			Name:    "Pterodactyl wings",
			Version: constants.Version,
		}
		info.System.SystemType = hostInfo.OS
		info.System.Platform = hostInfo.Platform
		info.System.Release = hostInfo.KernelVersion
		info.System.Cpus = cpuInfo[0].Cores
		info.System.Freemem = memInfo.Free

		c.JSON(http.StatusOK, info)
		return
	}

	c.Header("Content-Type", "text/html")
	c.String(http.StatusOK, constants.IndexPage)
}

// handlePatchConfig handles PATCH /config
func PatchConfiguration(c *gin.Context) {

}
