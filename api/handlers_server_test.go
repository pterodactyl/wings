package api

import (
	"bytes"
	"encoding/json"
	"io/ioutil"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Pterodactyl/wings/control"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHandleGetServers(t *testing.T) {
	router := gin.New()
	req, _ := http.NewRequest("GET", "/servers", nil)

	router.GET("/servers", handleGetServers)

	t.Run("returns an empty json array when no servers are configured", func(t *testing.T) {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		body, err := ioutil.ReadAll(rec.Body)
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Nil(t, err)
		assert.Equal(t, "[]\n", string(body))
	})

	t.Run("returns an array of servers", func(t *testing.T) {

	})
}

var testServer = control.ServerStruct{
	ID: "id1",
}

func TestHandlePostServer(t *testing.T) {
	// We need to decide how deep we want to go with testing here
	// Should we just verify it works in general or test validation and
	// minimal required config options as well?
	// This will also vary depending on the Environment the server runs in
}

func TestHandleGetServer(t *testing.T) {
	router := gin.New()
	router.GET("/servers/:server", handleGetServer)

	control.CreateServer(&testServer)

	t.Run("returns not found when the server doesn't exist", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/servers/id0", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("returns the server as json", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/servers/id1", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func TestHandlePatchServer(t *testing.T) {

}

func TestHandleDeleteServer(t *testing.T) {
	router := gin.New()
	router.DELETE("/servers/:server", handleDeleteServer)

	control.CreateServer(&testServer)

	t.Run("returns not found when the server doesn't exist", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/servers/id0", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("deletes the server", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", "/servers/id1", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		assert.Equal(t, http.StatusOK, rec.Code)
	})
}

func bodyToJSON(body *bytes.Buffer, v interface{}) error {
	reqBody, err := ioutil.ReadAll(body)
	if err != nil {
		return err
	}
	err = json.Unmarshal(reqBody, v)
	if err != nil {
		return err
	}
	return nil
}
