package config

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

var configFile = "../config.example.json"

func TestLoadConfiguraiton(t *testing.T) {
	err := LoadConfiguration(&configFile)
	assert.Nil(t, err)
	assert.Equal(t, Get().Web.ListenHost, "0.0.0.0")
}
