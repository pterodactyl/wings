package api

import (
	"fmt"
	"net/http"
	//"runtime"

	"github.com/gin-gonic/gin"
	"github.com/shirou/gopsutil/cpu"
	//"github.com/shirou/gopsutil/host"
	//"github.com/shirou/gopsutil/mem"
	log "github.com/sirupsen/logrus"
)

func GetIndex(c *gin.Context) {
	//auth := GetContextAuthManager(c)
	//if auth == nil {
	//	c.Header("Content-Type", "text/html")
	//	c.String(http.StatusOK, constants.IndexPage)
	//}

	s, err := cpu.Counts(true)
	if err != nil {
		log.WithError(err).Error("Failed to retrieve host information.")
	}

	fmt.Println(s)
	i := struct {
		Name string
		Cpu  struct {
			Cores int
		}
	}{
		Name: "Wings",
	}

	i.Cpu.Cores = s

	c.JSON(http.StatusOK, i)
	return

	//if auth != nil && auth.HasPermission("c:info") {
	//	hostInfo, err := host.Info()
	//	if err != nil {
	//		log.WithError(err).Error("Failed to retrieve host information.")
	//	}
	//	cpuInfo, err := cpu.Info()
	//	if err != nil {
	//		log.WithError(err).Error("Failed to retrieve CPU information.")
	//	}
	//	memInfo, err := mem.VirtualMemory()
	//	if err != nil {
	//		log.WithError(err).Error("Failed to retrieve memory information.")
	//	}
	//
	//	info := struct {
	//		Name    string `json:"name"`
	//		Version string `json:"version"`
	//		System  struct {
	//			SystemType string `json:"type"`
	//			Platform   string `json:"platform"`
	//			Arch       string `json:"arch"`
	//			Release    string `json:"release"`
	//			Cpus       int32  `json:"cpus"`
	//			Freemem    uint64 `json:"freemem"`
	//		} `json:"system"`
	//	}{
	//		Name:    "Pterodactyl wings",
	//		Version: constants.Version,
	//	}
	//	info.System.SystemType = hostInfo.OS
	//	info.System.Platform = hostInfo.Platform
	//	info.System.Arch = runtime.GOARCH
	//	info.System.Release = hostInfo.KernelVersion
	//	info.System.Cpus = cpuInfo[0].Cores
	//	info.System.Freemem = memInfo.Free
	//
	//	c.JSON(http.StatusOK, info)
	//	return
	//}
}

type incomingConfiguration struct {
	Debug bool `mapstructure:"debug"`
	Web   struct {
		ListenHost string `mapstructure:"host"`
		ListenPort int16  `mapstructure:"port"`
		SSL        struct {
			Enabled     bool   `mapstructure:"enabled"`
			Certificate string `mapstructure:"certificate"`
			Key         string `mapstructure:"key"`
		} `mapstructure:"ssl"`

		Uploads struct {
			MaximumSize int64 `mapstructure:"maximumSize"`
		} `mapstructure:"uploads"`
	} `mapstructure:"web"`

	Docker struct {
		Socket           string `mapstructure:"socket"`
		AutoupdateImages bool   `mapstructure:"autoupdateImages"`
		NetworkInterface string `mapstructure:"networkInterface"`
		TimezonePath     string `mapstructure:"timezonePath"`
	} `mapstructure:"docker"`

	Sftp struct {
		Path string `mapstructure:"path"`
		Port int16  `mapstructure:"port"`
	} `mapstructure:"sftp"`

	Query struct {
		KillOnFail bool `mapstructure:"killOnFail"`
		FailLimit  bool `mapstructure:"failLimit"`
	} `mapstructure:"query"`

	Remote string `mapstructure:"remote"`

	Log struct {
		Path            string `mapstructure:"path"`
		Level           string `mapstructure:"level"`
		DeleteAfterDays int    `mapstructure:"deleteAfterDays"`
	} `mapstructure:"log"`

	AuthKeys []string `mapstructure:"authKeys"`
}

// handlePatchConfig handles PATCH /config
func PatchConfiguration(c *gin.Context) {
	// reqBody, err := ioutil.ReadAll(c.Request.Body)
	// if err != nil {
	// 	log.WithError(err).Error("Failed to read input.")
	// 	return
	// }
	// reqJSON := new(incomingConfiguration)
	// err = json.Unmarshal(reqBody, reqJSON)
	// if err != nil {
	// 	log.WithError(err).Error("Failed to decode JSON.")
	// 	return
	// }
	var json incomingConfiguration
	if err := c.BindJSON(&json); err != nil {
		log.WithError(err).Error("Failed to bind Json.")
	}
}
