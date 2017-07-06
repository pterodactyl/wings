package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

const configFile = "../config.example.json"

func TestLoadConfiguraiton(t *testing.T) {
	err := LoadConfiguration(configFile)
	assert.Nil(t, err)
	assert.Equal(t, Get().Web.ListenHost, "0.0.0.0")
}

func TestContainsAuthKey(t *testing.T) {
	t.Run("key exists", func(t *testing.T) {
		LoadConfiguration(configFile)
		assert.True(t, Get().ContainsAuthKey("somekey"))
	})

	t.Run("key doesn't exist", func(t *testing.T) {
		LoadConfiguration(configFile)
		assert.False(t, Get().ContainsAuthKey("someotherkey"))
	})
}
