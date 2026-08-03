// Package ui serves the embedded read-only Kasim inventory on strict
// loopback. It intentionally exposes no remote binding or mutation surface.
package ui

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	"github.com/LinkMaq/kube-accelerator-sim/internal/inventory"
)

const contentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self' data:; frame-ancestors 'none'; object-src 'none'; base-uri 'none'; form-action 'none'"

type Options struct {
	Module *inventory.Module
	Target cluster.TargetSelection
	Port   int
}

type Server struct {
	listener net.Listener
	http     *http.Server
	stream   inventory.SnapshotStream
	store    *snapshotStore
	token    string
	url      string
}

func NewServer(ctx context.Context, options Options) (*Server, error) {
	if options.Module == nil {
		return nil, fmt.Errorf("Cluster Simulation Inventory Module is required")
	}
	if options.Port < 1 || options.Port > 65535 {
		return nil, fmt.Errorf("port must be between 1 and 65535")
	}
	stream, err := options.Module.Open(ctx, inventory.OpenRequest{Target: options.Target})
	if err != nil {
		return nil, err
	}
	loading, err := stream.Next(ctx)
	if err != nil {
		stream.Close()
		return nil, err
	}
	token, err := capabilityToken()
	if err != nil {
		stream.Close()
		return nil, err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:"+strconv.Itoa(options.Port))
	if err != nil {
		stream.Close()
		return nil, fmt.Errorf("listen on 127.0.0.1:%d: %w", options.Port, err)
	}
	store := newSnapshotStore(loading)
	server := &Server{
		listener: listener,
		stream:   stream,
		store:    store,
		token:    token,
		url:      "http://" + listener.Addr().String() + "/#token=" + token,
	}
	server.http = &http.Server{
		Handler:           server.handler(listener.Addr().String()),
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       65 * time.Second,
		MaxHeaderBytes:    16 << 10,
	}
	return server, nil
}

func (server *Server) AccessURL() string { return server.url }

func (server *Server) Target() inventory.Target { return server.store.current().Target }

func (server *Server) Serve(ctx context.Context) error {
	collectorCtx, cancelCollector := context.WithCancel(ctx)
	collectorDone := make(chan struct{})
	go func() {
		defer close(collectorDone)
		server.collect(collectorCtx)
	}()
	defer func() {
		cancelCollector()
		_ = server.stream.Close()
		<-collectorDone
	}()

	serveResult := make(chan error, 1)
	go func() { serveResult <- server.http.Serve(server.listener) }()
	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := server.http.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shut down kasim ui: %w", err)
		}
		err := <-serveResult
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}

func (server *Server) collect(ctx context.Context) {
	for {
		snapshot, err := server.stream.Next(ctx)
		if err != nil {
			if ctx.Err() == nil {
				current := server.store.current()
				current.Revision++
				current.Freshness = inventory.FreshnessStale
				current.Completeness = inventory.CompletenessPartial
				current.Diagnostics = append(current.Diagnostics, inventory.Diagnostic{
					Code:    "InventoryStreamStopped",
					Message: "inventory synchronization stopped; sensitive details were redacted",
				})
				server.store.update(current)
			}
			return
		}
		server.store.update(snapshot)
	}
}

func (server *Server) handler(expectedHost string) http.Handler {
	assets, err := fs.Sub(embeddedAssets, "static")
	if err != nil {
		panic(err)
	}
	static := http.FileServer(http.FS(assets))
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		setSecurityHeaders(response.Header())
		if request.Host != expectedHost {
			http.Error(response, "misdirected loopback request", http.StatusMisdirectedRequest)
			return
		}
		if request.Method != http.MethodGet && request.Method != http.MethodHead {
			response.Header().Set("Allow", "GET, HEAD")
			http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		switch request.URL.Path {
		case "/api/v1/snapshot":
			if !server.authorized(request) {
				response.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(response, "inventory capability required", http.StatusUnauthorized)
				return
			}
			response.Header().Set("Content-Type", "application/json; charset=utf-8")
			_ = json.NewEncoder(response).Encode(server.store.current())
		case "/api/v1/stream":
			if !server.authorized(request) {
				response.Header().Set("WWW-Authenticate", "Bearer")
				http.Error(response, "inventory capability required", http.StatusUnauthorized)
				return
			}
			server.streamSnapshots(response, request)
		case "/", "/index.html", "/assets/app.css", "/assets/app.js":
			path := request.URL.Path
			if path == "/assets/app.css" {
				request.URL.Path = "/app.css"
			} else if path == "/assets/app.js" {
				request.URL.Path = "/app.js"
			}
			static.ServeHTTP(response, request)
		default:
			http.NotFound(response, request)
		}
	})
}

func (server *Server) streamSnapshots(response http.ResponseWriter, request *http.Request) {
	flusher, ok := response.(http.Flusher)
	if !ok {
		http.Error(response, "streaming unavailable", http.StatusInternalServerError)
		return
	}
	response.Header().Set("Content-Type", "application/x-ndjson; charset=utf-8")
	response.Header().Set("X-Accel-Buffering", "no")
	encoder := json.NewEncoder(response)
	for {
		snapshot, changed := server.store.read()
		if err := encoder.Encode(snapshot); err != nil {
			return
		}
		flusher.Flush()
		select {
		case <-changed:
		case <-request.Context().Done():
			return
		}
	}
}

func (server *Server) authorized(request *http.Request) bool {
	const prefix = "Bearer "
	value := request.Header.Get("Authorization")
	if len(value) <= len(prefix) || value[:len(prefix)] != prefix {
		return false
	}
	provided := value[len(prefix):]
	return len(provided) == len(server.token) &&
		subtle.ConstantTimeCompare([]byte(provided), []byte(server.token)) == 1
}

func capabilityToken() (string, error) {
	material := make([]byte, 32)
	if _, err := rand.Read(material); err != nil {
		return "", fmt.Errorf("generate local UI capability: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(material), nil
}

func setSecurityHeaders(header http.Header) {
	header.Set("Cache-Control", "no-store")
	header.Set("Content-Security-Policy", contentSecurityPolicy)
	header.Set("Cross-Origin-Resource-Policy", "same-origin")
	header.Set("Referrer-Policy", "no-referrer")
	header.Set("X-Content-Type-Options", "nosniff")
	header.Set("X-Frame-Options", "DENY")
}

type snapshotStore struct {
	mu      sync.RWMutex
	latest  inventory.Snapshot
	changed chan struct{}
}

func newSnapshotStore(snapshot inventory.Snapshot) *snapshotStore {
	return &snapshotStore{latest: cloneSnapshot(snapshot), changed: make(chan struct{})}
}

func (store *snapshotStore) current() inventory.Snapshot {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return cloneSnapshot(store.latest)
}

func (store *snapshotStore) read() (inventory.Snapshot, <-chan struct{}) {
	store.mu.RLock()
	defer store.mu.RUnlock()
	return cloneSnapshot(store.latest), store.changed
}

func (store *snapshotStore) update(snapshot inventory.Snapshot) {
	store.mu.Lock()
	store.latest = cloneSnapshot(snapshot)
	close(store.changed)
	store.changed = make(chan struct{})
	store.mu.Unlock()
}

func cloneSnapshot(snapshot inventory.Snapshot) inventory.Snapshot {
	snapshot.Sources = append([]inventory.SourceState(nil), snapshot.Sources...)
	snapshot.Diagnostics = append([]inventory.Diagnostic(nil), snapshot.Diagnostics...)
	nodes := make([]inventory.Node, 0, len(snapshot.Nodes))
	for _, node := range snapshot.Nodes {
		signals := make([]inventory.Signal, 0, len(node.Signals))
		for _, signal := range node.Signals {
			if signal.Device != nil {
				device := *signal.Device
				signal.Device = &device
			}
			signal.Associations = append([]string(nil), signal.Associations...)
			if signal.Attributes != nil {
				attributes := make(map[string]string, len(signal.Attributes))
				for key, value := range signal.Attributes {
					attributes[key] = value
				}
				signal.Attributes = attributes
			}
			signals = append(signals, signal)
		}
		node.Signals = signals
		nodes = append(nodes, node)
	}
	snapshot.Nodes = nodes
	return snapshot
}
