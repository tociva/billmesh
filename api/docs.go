// Package apidocs embeds Billmesh's OpenAPI contract and its Swagger UI.
package apidocs

import (
	_ "embed"
	"html/template"
	"net/http"

	swaggerui "github.com/swaggest/swgui/v5emb"
)

//go:embed openapi.yaml
var specification []byte

var swaggerHandler = swaggerui.New("Billmesh API", "/openapi.yaml", "/docs/")

var accessPage = template.Must(template.New("access").Parse(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Billmesh API documentation</title>
  <link rel="stylesheet" href="/docs/swagger-ui.css">
  <style>
    body { margin: 0; background: #f7f8fa; font-family: system-ui, sans-serif; color: #1f2937; }
    #access { max-width: 720px; margin: 12vh auto; padding: 2rem; background: white; border-radius: 12px; box-shadow: 0 8px 30px #0001; }
    h1 { margin-top: 0; } label { display: block; font-weight: 600; margin-bottom: .5rem; }
    input { box-sizing: border-box; width: 100%; padding: .75rem; font: inherit; }
    button { margin: 1rem .5rem 0 0; padding: .7rem 1rem; font: inherit; cursor: pointer; }
    #message { color: #b42318; min-height: 1.5rem; } #toolbar { display: none; padding: .75rem 1rem; background: #fff; }
  </style>
</head>
<body>
  <main id="access">
    <h1>Billmesh API documentation</h1>
    <p>Enter an access token. It is kept only in this page's memory and is never added to the URL or browser storage.</p>
    <form id="token-form">
      <label for="token">Bearer token</label>
      <input id="token" name="token" type="password" autocomplete="off" required>
      <button type="submit">Open documentation</button>
    </form>
    <p id="message" role="alert"></p>
  </main>
  <div id="toolbar"><button id="download" type="button">Download OpenAPI YAML</button><button id="clear" type="button">Clear token</button></div>
  <div id="swagger-ui"></div>
  <script src="/docs/swagger-ui-bundle.js"></script>
  <script src="/docs/swagger-ui-standalone-preset.js"></script>
  <script>
    (() => {
      let bearer = '';
      const form = document.getElementById('token-form');
      const message = document.getElementById('message');
      const authenticatedFetch = () => fetch('/openapi.yaml', {
        headers: { Authorization: bearer }, credentials: 'omit', cache: 'no-store'
      });
      form.addEventListener('submit', async event => {
        event.preventDefault();
        const value = document.getElementById('token').value.trim().replace(/^Bearer\s+/i, '');
        bearer = value ? 'Bearer ' + value : '';
        const response = await authenticatedFetch().catch(() => null);
        if (!response || !response.ok) {
          bearer = '';
          message.textContent = response ? 'Access denied (' + response.status + ').' : 'Unable to reach the API.';
          return;
        }
        document.getElementById('token').value = '';
        document.getElementById('access').hidden = true;
        document.getElementById('toolbar').style.display = 'block';
        SwaggerUIBundle({
          url: '/openapi.yaml', dom_id: '#swagger-ui', deepLinking: true,
          presets: [SwaggerUIBundle.presets.apis, SwaggerUIStandalonePreset], layout: 'StandaloneLayout',
          requestInterceptor: request => {
            const target = new URL(request.url, window.location.href);
            if (target.origin === window.location.origin && (target.pathname === '/openapi.yaml' || target.pathname.startsWith('/v1/'))) {
              request.headers.Authorization = bearer;
            }
            return request;
          }
        });
      });
      document.getElementById('download').addEventListener('click', async () => {
        const response = await authenticatedFetch();
        if (!response.ok) { message.textContent = 'Download denied (' + response.status + ').'; return; }
        const link = document.createElement('a');
        link.href = URL.createObjectURL(await response.blob());
        link.download = 'billmesh-openapi.yaml'; link.click(); URL.revokeObjectURL(link.href);
      });
      document.getElementById('clear').addEventListener('click', () => window.location.reload());
    })();
  </script>
</body>
</html>`))

// Specification returns a copy of the embedded OpenAPI document.
func Specification() []byte {
	return append([]byte(nil), specification...)
}

// SpecificationHandler serves the raw OpenAPI document.
func SpecificationHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	_, _ = w.Write(specification)
}

// AccessHandler serves a public credential launcher. It contains no API
// contract and keeps the supplied bearer token only in JavaScript memory.
func AccessHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "private, no-store")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_ = accessPage.Execute(w, nil)
}

// SwaggerHandler serves Swagger UI and all of its assets from the binary.
func SwaggerHandler() http.Handler {
	return swaggerHandler
}
