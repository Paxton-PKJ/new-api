package middleware

import (
	"mime"
	"net/url"
	"strings"

	"github.com/QuantumNous/new-api/common"
	"github.com/QuantumNous/new-api/constant"
	"github.com/QuantumNous/new-api/logger"
	"github.com/QuantumNous/new-api/model"

	"github.com/gin-gonic/gin"
)

// ApplyTokenModelMapping applies the token-level model redirect before routing:
// a client model that the token maps to a logical model is rewritten at its
// request source, so Model Limits, AutoGroups, channel selection, channel-level
// model mapping, billing, and logs all see the logical model. Shared with the
// Responses WebSocket relay (relay package).
//
// The rewrite is skipped without side effects when the request cannot be
// rewritten safely (multipart, no client model to replace, Midjourney actions,
// task-plugin-claimed models, fetch/notify paths the caller never routes);
// the original model then stays in place end to end. A cyclic mapping returns
// model.ErrTokenModelMappingCycle for the caller to answer with 400.
func ApplyTokenModelMapping(c *gin.Context, modelRequest *ModelRequest) error {
	mapping, ok := common.GetContextKeyType[map[string]string](c, constant.ContextKeyTokenModelMapping)
	if !ok || len(mapping) == 0 || modelRequest == nil || modelRequest.Model == "" {
		return nil
	}
	if c.GetString("resolved_task_model") != "" {
		// A task plugin already claimed and validated the model; the logical
		// model it resolved is not subject to the token mapping.
		return nil
	}
	if strings.Contains(c.Request.URL.Path, "/mj/") {
		// Midjourney models are derived from the action, not from the request.
		return nil
	}
	target, err := model.ResolveTokenModelMapping(mapping, modelRequest.Model)
	if err != nil {
		return err
	}
	if target == modelRequest.Model {
		return nil
	}
	rewritten, err := rewriteRequestModel(c, target)
	if err != nil {
		return err
	}
	if !rewritten {
		logger.LogDebug(c, "token model mapping skipped: request model source cannot be rewritten (%s)", c.Request.URL.Path)
		return nil
	}
	common.SetContextKey(c, constant.ContextKeyTokenModelMappingClientModel, modelRequest.Model)
	logger.LogDebug(c, "token model mapping applied: %s -> %s", modelRequest.Model, target)
	modelRequest.Model = target
	if cached, exists := c.Get(contextKeyTaskPluginEndpointModel); exists {
		// A shared task-plugin endpoint cached the distribution model before
		// Distribute ran; keep both views on the logical model.
		if cachedRequest, ok := cached.(ModelRequest); ok {
			cachedRequest.Model = target
			c.Set(contextKeyTaskPluginEndpointModel, cachedRequest)
		}
	}
	return nil
}

// rewriteRequestModel rewrites the model at the source the request actually
// carries it in: the Gemini URL path, the realtime query string, the JSON body,
// or an urlencoded form. It reports false when the request has no rewritable
// model source, which leaves the original model untouched.
func rewriteRequestModel(c *gin.Context, modelName string) (bool, error) {
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/v1beta/models/") || strings.HasPrefix(path, "/v1/models/") {
		return rewriteGeminiPathModel(c, modelName)
	}
	if strings.HasPrefix(path, "/v1/realtime") {
		return rewriteQueryModel(c, modelName)
	}
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err == nil {
		if mediaType == "application/json" || strings.HasSuffix(mediaType, "+json") {
			return rewriteJSONBodyModel(c, modelName)
		}
		if mediaType == gin.MIMEPOSTForm {
			return rewriteFormBodyModel(c, modelName)
		}
	}
	return false, nil
}

// rewriteGeminiPathModel replaces the model segment of a Gemini path and keeps
// the ":action" suffix and the query string.
func rewriteGeminiPathModel(c *gin.Context, modelName string) (bool, error) {
	if strings.ContainsAny(modelName, "/:") {
		// The {model} placeholder of an advanced custom route cannot contain
		// "/", and ":" would merge with the ":action" suffix.
		return false, nil
	}
	const modelsPrefix = "/models/"
	path := c.Request.URL.Path
	modelsIndex := strings.Index(path, modelsPrefix)
	if modelsIndex == -1 {
		return false, nil
	}
	startIndex := modelsIndex + len(modelsPrefix)
	endIndex := len(path)
	if colonIndex := strings.Index(path[startIndex:], ":"); colonIndex != -1 {
		endIndex = startIndex + colonIndex
	}
	if startIndex >= endIndex {
		return false, nil
	}
	c.Request.URL.Path = path[:startIndex] + modelName + path[endIndex:]
	c.Request.URL.RawPath = ""
	c.Request.RequestURI = c.Request.URL.RequestURI()
	return true, nil
}

// rewriteQueryModel replaces the model of a realtime session query string.
func rewriteQueryModel(c *gin.Context, modelName string) (bool, error) {
	query := c.Request.URL.Query()
	if !query.Has("model") {
		return false, nil
	}
	query.Set("model", modelName)
	c.Request.URL.RawQuery = query.Encode()
	c.Request.RequestURI = c.Request.URL.RequestURI()
	return true, nil
}

// rewriteFormBodyModel re-encodes an urlencoded form with the new model.
func rewriteFormBodyModel(c *gin.Context, modelName string) (bool, error) {
	storage, err := common.GetBodyStorage(c)
	if err != nil {
		return false, err
	}
	raw, err := storage.Bytes()
	if err != nil {
		return false, err
	}
	values, err := url.ParseQuery(string(raw))
	if err != nil {
		return false, nil
	}
	if !values.Has("model") {
		return false, nil
	}
	values.Set("model", modelName)
	if err := replaceBodyStorage(c, []byte(values.Encode())); err != nil {
		return false, err
	}
	return true, nil
}
