# ADR 0010: Embed an authenticated loopback UI in kasim

Status: Accepted

`kasim ui` is a temporary, read-only view of one resolved Simulation Target, not a persistent management service. The released `kasim` binary will embed standards-based HTML, CSS, and JavaScript assets and serve them only from `127.0.0.1`. It will not require a Node.js runtime, an external asset directory, another process, a Helm component, remote authentication, or a non-loopback listen option.

Every launch generates a high-entropy ephemeral capability token. The printed and optional browser-open URL carries it in the fragment so it is not sent in HTTP requests, logs, or referrers; the frontend supplies it as a bearer credential to every data request. Static assets reveal no cluster data. The server validates the exact loopback Host, omits permissive CORS, rejects non-GET/HEAD methods, sets a strict Content Security Policy and same-origin framing/resource headers, disables data caching, and exposes no mutation route. The browser JSON and streaming transport are versioned implementation details of the same `kasim` process, not a supported remote public interface.

The command follows standard client-go/kubectl kubeconfig loading rules and current-context when target flags are absent. `--kubeconfig` and `--context` independently override those defaults, and supplying both preserves exact-target selection. The resolved context and client configuration are frozen before the listener opens; this exception applies only to the read-only UI, while lifecycle commands retain mandatory explicit target flags. The command defaults to `127.0.0.1:8080`, supports `--port` and `--open`, and has no `--address`. A bind conflict or failed target establishment returns a stable startup diagnostic and closes the listener. Browser-open failure is a warning because the printed URL remains usable. `SIGINT` and `SIGTERM` cancel inventory synchronization, stop accepting requests, and perform a bounded graceful shutdown. Ordinary source permission failures or unsupported optional APIs do not stop the server; the page opens in partial or diagnostics-only mode.

## Considered options

- An unauthenticated localhost API was rejected because other local origins and DNS-rebinding-style requests should not receive cluster metadata merely because the listener is on loopback.
- Cookies and a login flow were rejected because there is no remote user or persistent server; an in-memory capability is smaller and avoids CSRF state.
- SSE with a query-string token was rejected because the credential would appear in request URLs. The implementation may use authenticated fetch streaming with polling fallback.
- A remotely bindable or Helm-deployed dashboard was rejected because it would require a different authentication, authorization, lifecycle, threat, and support model.
