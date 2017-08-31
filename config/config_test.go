package config

import (
	"testing"

	"github.com/spf13/viper"

	"github.com/stretchr/testify/assert"
)

const configFile = "../config.example.json"

func TestLoadConfiguraiton(t *testing.T) {
	err := LoadConfiguration(configFile)
	assert.Nil(t, err)
	assert.Equal(t, "0.0.0.0", viper.GetString(APIHost))
}

func TestContainsAuthKey(t *testing.T) {
	t.Run("key exists", func(t *testing.T) {
		LoadConfiguration(configFile)
		assert.True(t, ContainsAuthKey("somekey"))
	})

	t.Run("key doesn't exist", func(t *testing.T) {
		LoadConfiguration(configFile)
		assert.False(t, ContainsAuthKey("someotherkey"))
	})
}
