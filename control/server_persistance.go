package control

import (
	"encoding/json"
	"io/ioutil"
	"os"
	"path/filepath"

	"github.com/pterodactyl/wings/config"
	"github.com/pterodactyl/wings/constants"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/viper"
)

// LoadServerConfigurations loads the configured servers from a specified path
func LoadServerConfigurations(path string) error {
	serverFiles, err := ioutil.ReadDir(path)
	if err != nil {
		return err
	}
	servers = make(serversMap)

	for _, file := range serverFiles {
		if file.IsDir() {
			server, err := loadServerConfiguration(filepath.Join(path, file.Name(), constants.ServerConfigFile))
			if err != nil {
				return err
			}
			servers[server.ID] = server
		}
	}

	return nil
}

func loadServerConfiguration(path string) (*ServerStruct, error) {
	file, err := ioutil.ReadFile(path)

	if err != nil {
		return nil, err
	}

	server := &ServerStruct{}
	if err := json.Unmarshal(file, server); err != nil {
		return nil, err
	}
	if err := server.init(); err != nil {
		return nil, err
	}
	return server, nil
}

func storeServerConfiguration(server *ServerStruct) error {
	serverJSON, err := json.MarshalIndent(server, "", constants.JSONIndent)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(server.path(), constants.DefaultFolderPerms); err != nil {
		return err
	}
	if err := ioutil.WriteFile(server.configFilePath(), serverJSON, constants.DefaultFilePerms); err != nil {
		return err
	}
	return nil
}

func storeServerConfigurations() error {
	for _, s := range servers {
		if err := storeServerConfiguration(s); err != nil {
			return err
		}
	}
	return nil
}

func deleteServerFolder(id string) error {
	path := filepath.Join(viper.GetString(config.DataPath), constants.ServersPath, id)
	folder, err := os.Stat(path)
	if os.IsNotExist(err) || !folder.IsDir() {
		return err
	}
	return os.RemoveAll(path)
}

func (s *ServerStruct) Save() error {
	if err := storeServerConfiguration(s); err != nil {
		log.WithField("server", s.ID).WithError(err).Error("Failed to store server configuration.")
		return err
	}
	return nil
}

func (s *ServerStruct) path() string {
	p, err := filepath.Abs(viper.GetString(config.DataPath))
	if err != nil {
		log.WithError(err).WithField("server", s.ID).Error("Failed to get absolute data path for server.")
		p = viper.GetString(config.DataPath)
	}
	return filepath.Join(p, constants.ServersPath, s.ID)
}

func (s *ServerStruct) dataPath() string {
	return filepath.Join(s.path(), constants.ServerDataPath)
}

func (s *ServerStruct) configFilePath() string {
	return filepath.Join(s.path(), constants.ServerConfigFile)
}
