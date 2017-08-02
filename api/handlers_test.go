package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestHandleGetIndex(t *testing.T) {
	router := gin.New()
	recorder := httptest.NewRecorder()
	req, _ := http.NewRequest("GET", "/", nil)

	router.GET("/", handleGetIndex)
	router.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestHandlePatchConfig(t *testing.T) {
	router := gin.New()
	recorder := httptest.NewRecorder()

	req, _ := http.NewRequest("PATCH", "/", strings.NewReader("{}"))

	router.PATCH("/", handlePatchConfig)
	router.ServeHTTP(recorder, req)

	assert.Equal(t, http.StatusOK, recorder.Code)
}
