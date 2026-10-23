package openai

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/tidwall/gjson"
)

func (h *OpenAIAPIHandler) Search(c *gin.Context) {
	h.openAICompatPassthrough(c, http.MethodPost)
}

func (h *OpenAIAPIHandler) PPTGenerations(c *gin.Context) {
	h.openAICompatPassthrough(c, http.MethodPost)
}

func (h *OpenAIAPIHandler) PSDGenerations(c *gin.Context) {
	h.openAICompatPassthrough(c, http.MethodPost)
}

func (h *OpenAIAPIHandler) EditableFileTasks(c *gin.Context) {
	h.openAICompatPassthrough(c, http.MethodGet)
}

func (h *OpenAIAPIHandler) FilesDownload(c *gin.Context) {
	h.openAICompatPassthrough(c, http.MethodGet)
}

func (h *OpenAIAPIHandler) openAICompatPassthrough(c *gin.Context, method string) {
	rawJSON, err := handlers.ReadRequestBody(c)
	if err != nil {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: fmt.Sprintf("Invalid request: %v", err),
				Type:    "invalid_request_error",
			},
		})
		return
	}

	var modelName string
	if method == http.MethodGet {
		modelName = strings.TrimSpace(c.Query("model"))
	} else {
		modelName = strings.TrimSpace(gjson.GetBytes(rawJSON, "model").String())
	}
	if modelName == "" {
		c.JSON(http.StatusBadRequest, handlers.ErrorResponse{
			Error: handlers.ErrorDetail{
				Message: "Invalid request: model is required",
				Type:    "invalid_request_error",
			},
		})
		return
	}

	cliCtx, cliCancel := h.GetContextWithCancel(h, c, context.Background())
	resp, upstreamHeaders, errMsg := h.ExecuteOpenAICompatPassthrough(
		cliCtx,
		modelName,
		rawJSON,
		method,
		c.Request.URL.Path,
		nil,
		c.Request.URL.Query(),
	)
	if errMsg != nil {
		h.WriteErrorResponse(c, errMsg)
		if errMsg.Error != nil {
			cliCancel(errMsg.Error)
		} else {
			cliCancel(nil)
		}
		return
	}

	handlers.WriteUpstreamHeaders(c.Writer.Header(), upstreamHeaders)
	_, _ = c.Writer.Write(resp)
	cliCancel(nil)
}
