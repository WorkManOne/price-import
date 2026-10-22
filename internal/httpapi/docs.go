package httpapi

import (
	"net/http"

	"github.com/WorkManOne/price-import/api"
)

func (a *API) openapi(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Write(api.Spec)
}

// swagger ui с cdn, чтоб не тащить статику в репо
const docsPage = `<!doctype html>
<html>
<head>
  <meta charset="utf-8">
  <title>price-import API</title>
  <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui.css">
</head>
<body>
  <div id="ui"></div>
  <script src="https://cdn.jsdelivr.net/npm/swagger-ui-dist@5.17.14/swagger-ui-bundle.js"></script>
  <script>SwaggerUIBundle({url: "/openapi.yaml", dom_id: "#ui"});</script>
</body>
</html>`

func (a *API) docs(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(docsPage))
}
