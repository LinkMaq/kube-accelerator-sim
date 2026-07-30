package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	pluginapi "k8s.io/kubelet/pkg/apis/deviceplugin/v1beta1"
)

const (
	defaultControlAddress = "127.0.0.1:18080"
	defaultEndpoint       = "kasim-oracle.sock"
	defaultResourceName   = "oracle.kasim.io/accelerator"
)

func main() {
	if err := runMain(os.Args[1:]); err != nil {
		log.Printf(`{"event":"fatal","error":%q}`, err.Error())
		os.Exit(1)
	}
}

func runMain(arguments []string) error {
	command := "serve"
	if len(arguments) > 0 && !strings.HasPrefix(arguments[0], "-") {
		command = arguments[0]
		arguments = arguments[1:]
	}
	switch command {
	case "serve":
		return serve(arguments)
	case "control":
		return control(arguments)
	case "hold":
		return hold()
	default:
		return fmt.Errorf("unknown command %q", command)
	}
}

func serve(arguments []string) error {
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	socketDirectory := flags.String(
		"socket-directory",
		pluginapi.DevicePluginPath,
		"kubelet Device Plugin socket directory",
	)
	resourceName := flags.String(
		"resource-name",
		defaultResourceName,
		"extended resource registered with kubelet",
	)
	controlAddress := flags.String(
		"control-address",
		defaultControlAddress,
		"loopback health control address",
	)
	deviceCount := flags.Int("devices", 2, "number of test-only devices")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *deviceCount < 1 {
		return errors.New("devices must be greater than zero")
	}

	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()

	logger := log.New(os.Stdout, "", 0)
	deviceIDs := make([]string, 0, *deviceCount)
	for index := range *deviceCount {
		deviceIDs = append(deviceIDs, fmt.Sprintf("kasim-oracle-%d", index))
	}
	plugin := newDevicePluginWithLogger(deviceIDs, logger)
	socketPath := filepath.Join(*socketDirectory, defaultEndpoint)
	if err := os.MkdirAll(*socketDirectory, 0o750); err != nil {
		return fmt.Errorf("create socket directory: %w", err)
	}
	if err := os.Remove(socketPath); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("remove stale oracle socket: %w", err)
	}

	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return fmt.Errorf("listen on oracle socket: %w", err)
	}
	if err := os.Chmod(socketPath, 0o600); err != nil {
		_ = listener.Close()
		_ = os.Remove(socketPath)
		return fmt.Errorf("protect oracle socket: %w", err)
	}
	grpcServer := grpc.NewServer()
	pluginapi.RegisterDevicePluginServer(grpcServer, plugin)
	serveErrors := make(chan error, 1)
	go func() {
		if serveErr := grpcServer.Serve(listener); serveErr != nil {
			serveErrors <- serveErr
		}
	}()
	defer func() {
		grpcServer.Stop()
		_ = listener.Close()
		_ = os.Remove(socketPath)
	}()

	if err := waitForPluginServer(ctx, socketPath); err != nil {
		return err
	}
	if err := registerWithKubelet(
		ctx,
		filepath.Join(*socketDirectory, "kubelet.sock"),
		*resourceName,
	); err != nil {
		return err
	}
	logger.Printf(
		`{"event":"registration","endpoint":%q,"resource":%q,"devices":%d}`,
		defaultEndpoint,
		*resourceName,
		*deviceCount,
	)

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(
		writer http.ResponseWriter,
		request *http.Request,
	) {
		if request.Method != http.MethodPost {
			http.Error(writer, "POST required", http.StatusMethodNotAllowed)
			return
		}
		if err := plugin.setHealth(request.URL.Query().Get("value")); err != nil {
			http.Error(writer, err.Error(), http.StatusBadRequest)
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintln(writer, `{"updated":true}`)
	})
	httpServer := &http.Server{
		Addr:              *controlAddress,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if serveErr := httpServer.ListenAndServe(); serveErr != nil &&
			!errors.Is(serveErr, http.ErrServerClosed) {
			serveErrors <- serveErr
		}
	}()

	select {
	case <-ctx.Done():
	case err := <-serveErrors:
		return fmt.Errorf("serve protocol oracle: %w", err)
	}
	shutdownContext, cancel := context.WithTimeout(
		context.Background(),
		5*time.Second,
	)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("stop control server: %w", err)
	}
	logger.Print(`{"event":"shutdown","socketRemoved":true}`)
	return nil
}

func waitForPluginServer(ctx context.Context, socketPath string) error {
	deadlineContext, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	for {
		connection, err := grpc.NewClient(
			"unix://"+socketPath,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err == nil {
			client := pluginapi.NewDevicePluginClient(connection)
			callContext, callCancel := context.WithTimeout(
				deadlineContext,
				time.Second,
			)
			_, callErr := client.GetDevicePluginOptions(
				callContext,
				&pluginapi.Empty{},
			)
			callCancel()
			_ = connection.Close()
			if callErr == nil {
				return nil
			}
		}
		select {
		case <-deadlineContext.Done():
			return fmt.Errorf(
				"wait for oracle gRPC socket %s: %w",
				socketPath,
				deadlineContext.Err(),
			)
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func registerWithKubelet(
	ctx context.Context,
	kubeletSocket string,
	resourceName string,
) error {
	deadlineContext, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	var lastError error
	for {
		connection, err := grpc.NewClient(
			"unix://"+kubeletSocket,
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		if err == nil {
			client := pluginapi.NewRegistrationClient(connection)
			callContext, callCancel := context.WithTimeout(
				deadlineContext,
				5*time.Second,
			)
			_, lastError = client.Register(
				callContext,
				&pluginapi.RegisterRequest{
					Version:      pluginapi.Version,
					Endpoint:     defaultEndpoint,
					ResourceName: resourceName,
					Options: &pluginapi.DevicePluginOptions{
						GetPreferredAllocationAvailable: true,
					},
				},
			)
			callCancel()
			_ = connection.Close()
			if lastError == nil {
				return nil
			}
		} else {
			lastError = err
		}
		select {
		case <-deadlineContext.Done():
			return fmt.Errorf(
				"register with kubelet socket %s: %v: %w",
				kubeletSocket,
				lastError,
				deadlineContext.Err(),
			)
		case <-time.After(500 * time.Millisecond):
		}
	}
}

func control(arguments []string) error {
	flags := flag.NewFlagSet("control", flag.ContinueOnError)
	health := flags.String("health", "", "Healthy or Unhealthy")
	address := flags.String(
		"control-address",
		defaultControlAddress,
		"loopback health control address",
	)
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if *health != pluginapi.Healthy && *health != pluginapi.Unhealthy {
		return fmt.Errorf(
			"health must be %q or %q",
			pluginapi.Healthy,
			pluginapi.Unhealthy,
		)
	}
	client := &http.Client{Timeout: 5 * time.Second}
	request, err := http.NewRequest(
		http.MethodPost,
		"http://"+*address+"/health?value="+*health,
		nil,
	)
	if err != nil {
		return err
	}
	response, err := client.Do(request)
	if err != nil {
		return fmt.Errorf("update oracle health: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("update oracle health: %s", response.Status)
	}
	return nil
}

func hold() error {
	ctx, stop := signal.NotifyContext(
		context.Background(),
		syscall.SIGINT,
		syscall.SIGTERM,
	)
	defer stop()
	<-ctx.Done()
	return nil
}
