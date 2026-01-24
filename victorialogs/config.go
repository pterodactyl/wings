package victorialogs

import "time"

type Config struct {
	Enabled       bool          `yaml:"enabled" json:"enabled" default:"false"`
	URL           string        `yaml:"url" json:"url" default:"http://localhost:9482"`
	Username      string        `yaml:"username" json:"username"`
	Password      string        `yaml:"password" json:"password"`
	Environment   string        `yaml:"environment" json:"environment" default:"production"`
	BatchSize     int           `yaml:"batch_size" json:"batch_size" default:"200"`
	FlushInterval time.Duration `yaml:"flush_interval" json:"flush_interval" default:"3s"`
}
