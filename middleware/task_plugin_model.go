package middleware

import (
	"io"
	"mime"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// rewriteJSONBodyModel patches the top-level JSON "model" field and replaces
// BodyStorage. It reports false without touching the request when the body is
// not JSON or carries no top-level string "model".
func rewriteJSONBodyModel(c *gin.Context, modelName string) (bool, error) {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		return false, nil
	}
	if mediaType != "application/json" && !strings.HasSuffix(mediaType, "+json") {
		return false, nil
	}
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return false, err
	}
	raw, err := storage.Bytes()
	if err != nil {
		return false, err
	}
	model := gjson.GetBytes(raw, "model")
	if !model.Exists() || model.Type != gjson.String {
		return false, nil
	}
	patched, err := sjson.SetBytes(raw, "model", modelName)
	if err != nil {
		return false, err
	}
	if err := replaceBodyStorage(c, patched); err != nil {
		return false, err
	}
	return true, nil
}

// replaceBodyStorage swaps the cached request body for data and resets Body and
// Content-Length. The storage it replaces is closed.
func replaceBodyStorage(c *gin.Context, data []byte) error {
	previous, err := common.GetBodyStorage(c)
	if err != nil {
		return err
	}
	newStorage, err := common.CreateBodyStorage(data)
	if err != nil {
		return err
	}
	_ = previous.Close()
	c.Set(common.KeyBodyStorage, newStorage)
	c.Set(common.KeyRequestBody, nil)
	c.Request.Body = io.NopCloser(newStorage)
	c.Request.ContentLength = int64(len(data))
	return nil
}
