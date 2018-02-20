package api

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/jsonapi"
	"github.com/pterodactyl/wings/control"
)

func getServerFromContext(context *gin.Context) control.Server {
	return control.GetServer(context.Param("server"))
}

func sendErrors(c *gin.Context, s int, err ...*jsonapi.ErrorObject) {
	c.Status(s)
	c.Header("Content-Type", "application/json")
	jsonapi.MarshalErrors(c.Writer, err)
}

func sendInternalError(c *gin.Context, title string, detail string) {
	sendErrors(c, http.StatusInternalServerError, &jsonapi.ErrorObject{
		Status: strconv.Itoa(http.StatusInternalServerError),
		Title:  title,
		Detail: detail,
	})
}

func sendForbidden(c *gin.Context) {
	sendErrors(c, http.StatusForbidden, &jsonapi.ErrorObject{
		Title:  "The provided token has insufficient permissions to perform this action.",
		Status: strconv.Itoa(http.StatusForbidden),
	})
}

func sendData(c *gin.Context, payload interface{}) {
	sendDataStatus(c, http.StatusOK, payload)
}

func sendDataStatus(c *gin.Context, status int, payload interface{}) {
	c.Status(status)
	c.Header("Content-Type", "application/json")
	jsonapi.MarshalPayload(c.Writer, payload)
}
