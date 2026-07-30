// Package cli delivers the concrete kasim command surface without introducing
// a plugin seam or reading implicit Kubernetes configuration.
package cli

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"strings"
	"time"

	"github.com/LinkMaq/kube-accelerator-sim/internal/application"
	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/cluster"
	clusterkubernetes "github.com/LinkMaq/kube-accelerator-sim/internal/cluster/kubernetes"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/presentation"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
	"github.com/LinkMaq/kube-accelerator-sim/internal/version"
)

const maximumScenarioBytes = 1 << 20
const creationIdentity = "kasim-cli/v1"

// Dependencies contains concrete delivery wiring used by tests. It does not
// add a product behavior seam; ScenarioRuntime still owns lifecycle behavior.
type Dependencies struct {
	Connect application.ConnectorFunc
	Catalog catalog.Snapshot
}

// Run executes one concrete CLI invocation and returns its stable exit
// category. Successful envelopes go to stdout; failures go to stderr.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return run(
		args,
		stdin,
		stdout,
		stderr,
		Dependencies{Connect: connectKubernetes},
	)
}

// RunWithDependencies exercises the same CLI delivery with concrete,
// process-local adapters.
func RunWithDependencies(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	dependencies Dependencies,
) int {
	return run(args, stdin, stdout, stderr, dependencies)
}

func run(
	args []string,
	stdin io.Reader,
	stdout, stderr io.Writer,
	dependencies Dependencies,
) int {
	format, err := requestedOutputFormat(args)
	if err != nil {
		human, _ := presentation.ParseOutputFormat("human")
		return writeFailure("", "InvocationInvalid", err.Error(), human, stderr)
	}
	if len(args) == 0 {
		return writeFailure(
			"",
			"InvocationInvalid",
			"usage: kasim <version|profile|apply>",
			format,
			stderr,
		)
	}

	snapshot := dependencies.Catalog
	if snapshot.Digest().String() == "" {
		snapshot, err = catalog.LoadBundled()
		if err != nil {
			return writeFailure(
				args[0],
				"CatalogInvalid",
				err.Error(),
				format,
				stderr,
			)
		}
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], snapshot, format, stdout, stderr)
	case "profile":
		return runProfile(args[1:], snapshot, format, stdout, stderr)
	case "apply":
		return runApply(
			args[1:],
			stdin,
			snapshot,
			dependencies.Connect,
			format,
			stdout,
			stderr,
		)
	case "status":
		return runStatus(args[1:], snapshot, dependencies.Connect, format, stdout, stderr)
	case "health":
		return runHealth(args[1:], snapshot, dependencies.Connect, format, stdout, stderr)
	case "scale":
		return runScale(args[1:], snapshot, dependencies.Connect, format, stdout, stderr)
	case "delete":
		return runDelete(args[1:], snapshot, dependencies.Connect, format, stdout, stderr)
	default:
		return writeFailure(
			args[0],
			"InvocationInvalid",
			fmt.Sprintf("unsupported command %q", args[0]),
			format,
			stderr,
		)
	}
}

func connectKubernetes(
	ctx context.Context,
	selection cluster.TargetSelection,
) (application.ConnectedTarget, error) {
	connection, err := clusterkubernetes.Connect(ctx, selection)
	if err != nil {
		return application.ConnectedTarget{}, err
	}
	return application.ConnectedTarget{
		Receipt:      connection.Receipt(),
		Target:       connection.Target(),
		ControlPlane: connection.ControlPlane(),
		Cluster:      connection.Cluster(),
	}, nil
}

func runVersion(
	args []string,
	snapshot catalog.Snapshot,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	flags, _ := outputFlagSet("version")
	if err := flags.Parse(args); err != nil {
		return writeFailure("version", "InvocationInvalid", err.Error(), format, stderr)
	}
	if flags.NArg() != 0 {
		return writeFailure(
			"version",
			"InvocationInvalid",
			"version accepts no positional arguments",
			format,
			stderr,
		)
	}
	build := version.Build("kasim", snapshot.Revision())
	return writeSuccess(presentation.Success(
		"Version",
		"version",
		presentation.VersionResult{
			Binary:            build.Binary,
			ProductVersion:    build.ProductVersion,
			SourceRevision:    build.SourceRevision,
			BuildDate:         build.BuildDate,
			SchemaVersion:     build.SchemaVersion,
			CatalogVersion:    build.CatalogVersion,
			KubernetesFloor:   build.KubernetesFloor,
			KubernetesCeiling: build.KubernetesCeiling,
		},
	), format, stdout, stderr)
}

func runProfile(
	args []string,
	snapshot catalog.Snapshot,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	if len(args) == 0 {
		return writeFailure(
			"profile",
			"InvocationInvalid",
			"usage: kasim profile <list|show>",
			format,
			stderr,
		)
	}
	switch args[0] {
	case "list":
		flags, _ := outputFlagSet("profile list")
		if err := flags.Parse(args[1:]); err != nil {
			return writeFailure("profile list", "InvocationInvalid", err.Error(), format, stderr)
		}
		if flags.NArg() != 0 {
			return writeFailure(
				"profile list",
				"InvocationInvalid",
				"profile list accepts no positional arguments",
				format,
				stderr,
			)
		}
		profiles := make([]presentation.ProfileSummaryResult, 0, len(snapshot.List()))
		for _, profile := range snapshot.List() {
			profiles = append(profiles, profileSummaryResult(profile))
		}
		return writeSuccess(presentation.Success(
			"ProfileList",
			"profile list",
			presentation.ProfileListResult{
				CatalogRevision: snapshot.Revision(),
				CatalogDigest:   snapshot.Digest().String(),
				Profiles:        profiles,
			},
		), format, stdout, stderr)
	case "show":
		flags, _ := outputFlagSet("profile show")
		showArgs := args[1:]
		profileID := ""
		if len(showArgs) != 0 && !strings.HasPrefix(showArgs[0], "-") {
			profileID = showArgs[0]
			showArgs = showArgs[1:]
		}
		if err := flags.Parse(showArgs); err != nil {
			return writeFailure("profile show", "InvocationInvalid", err.Error(), format, stderr)
		}
		if profileID == "" && flags.NArg() == 1 {
			profileID = flags.Arg(0)
		} else if flags.NArg() != 0 {
			return writeFailure(
				"profile show",
				"InvocationInvalid",
				"usage: kasim profile show <profile-id>",
				format,
				stderr,
			)
		}
		if profileID == "" {
			return writeFailure(
				"profile show",
				"InvocationInvalid",
				"usage: kasim profile show <profile-id>",
				format,
				stderr,
			)
		}
		profile, err := snapshot.Show(profileID)
		if err != nil {
			return writeFailure("profile show", "CatalogInvalid", err.Error(), format, stderr)
		}
		return writeSuccess(presentation.Success(
			"Profile",
			"profile show",
			profileResult(profile),
		), format, stdout, stderr)
	default:
		return writeFailure(
			"profile",
			"InvocationInvalid",
			fmt.Sprintf("unsupported profile command %q", args[0]),
			format,
			stderr,
		)
	}
}

func runApply(
	args []string,
	stdin io.Reader,
	snapshot catalog.Snapshot,
	connect application.ConnectorFunc,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	demo := len(args) != 0 && args[0] == "demo"
	if demo {
		args = args[1:]
	}
	flags := flag.NewFlagSet("apply", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var file, dryRun, output string
	var kubeconfigPath, contextName, instanceUIDValue string
	var profileID, modelID, contractID, resourceAlias, fidelity string
	var nodes, accelerators, healthy int64
	var expectedGeneration uint64
	var acceptProvisional bool
	var async bool
	var timeout time.Duration
	flags.StringVar(&file, "f", "", "local Scenario file or - for stdin")
	flags.StringVar(&dryRun, "dry-run", "", "client or server")
	flags.StringVar(&output, "o", "human", "human, json, or yaml")
	flags.StringVar(&output, "output", "human", "human, json, or yaml")
	flags.StringVar(&kubeconfigPath, "kubeconfig", "", "explicit kubeconfig path")
	flags.StringVar(&contextName, "context", "", "exact kubeconfig context")
	flags.StringVar(&instanceUIDValue, "instance-uid", "", "exact existing instance UID")
	flags.Uint64Var(
		&expectedGeneration,
		"expected-generation",
		0,
		"exact existing desired generation",
	)
	flags.BoolVar(&async, "async", false, "return after revision acceptance")
	flags.DurationVar(&timeout, "timeout", 5*time.Minute, "terminal wait timeout")
	flags.StringVar(&profileID, "profile", "", "Vendor Profile ID")
	flags.StringVar(&modelID, "model", "", "Accelerator Model ID")
	flags.StringVar(&contractID, "contract", "", "Resource Contract ID")
	flags.StringVar(&resourceAlias, "resource", "", "portable resource alias")
	flags.StringVar(&fidelity, "fidelity", "", "Fidelity Mode")
	flags.Int64Var(&nodes, "nodes", -1, "homogeneous Node count")
	flags.Int64Var(&accelerators, "accelerators-per-node", -1, "accelerators per Node")
	flags.Int64Var(&healthy, "healthy-per-node", -1, "healthy accelerators per Node")
	flags.BoolVar(
		&acceptProvisional,
		"accept-provisional",
		false,
		"accept provisional profile evidence",
	)
	if err := flags.Parse(args); err != nil {
		return writeFailure("apply", "InvocationInvalid", err.Error(), format, stderr)
	}
	if flags.NArg() != 0 {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"apply accepts one -f input or the demo shortcut, not additional arguments",
			format,
			stderr,
		)
	}
	if dryRun != "" && dryRun != "client" && dryRun != "server" {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"--dry-run accepts only client or server",
			format,
			stderr,
		)
	}
	if dryRun == "client" &&
		(kubeconfigPath != "" || contextName != "" || instanceUIDValue != "" ||
			expectedGeneration != 0 || async) {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"client dry-run is offline and does not accept target, revision, or async flags",
			format,
			stderr,
		)
	}
	if dryRun != "client" && (kubeconfigPath == "" || contextName == "") {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"connected apply requires --kubeconfig and --context",
			format,
			stderr,
		)
	}
	if dryRun == "server" && async {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"server dry-run cannot be asynchronous",
			format,
			stderr,
		)
	}
	if timeout <= 0 {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"--timeout must be positive",
			format,
			stderr,
		)
	}
	if (instanceUIDValue == "") != (expectedGeneration == 0) {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"--instance-uid and --expected-generation must be supplied together",
			format,
			stderr,
		)
	}
	if expectedGeneration > math.MaxInt64 {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"--expected-generation is too large",
			format,
			stderr,
		)
	}

	var input scenario.Input
	var err error
	if demo {
		if file != "" {
			return writeFailure(
				"apply",
				"InvocationInvalid",
				"-f and the demo shortcut are mutually exclusive",
				format,
				stderr,
			)
		}
		if profileID == "" || modelID == "" || nodes < 0 || accelerators < 0 {
			return writeFailure(
				"apply",
				"InvocationInvalid",
				"demo requires --profile, --model, --nodes, and --accelerators-per-node",
				format,
				stderr,
			)
		}
		var healthyPointer *int64
		if healthy >= 0 {
			healthyPointer = &healthy
		}
		input, err = scenario.Shortcut(scenario.ShortcutInput{
			Name:                       "demo",
			ProfileID:                  profileID,
			ModelID:                    modelID,
			ContractID:                 contractID,
			ResourceAlias:              resourceAlias,
			Fidelity:                   fidelity,
			Nodes:                      nodes,
			AcceleratorsPerNode:        accelerators,
			HealthyPerNode:             healthyPointer,
			AcceptsProvisionalProfiles: acceptProvisional,
		})
	} else {
		if file == "" {
			return writeFailure(
				"apply",
				"InvocationInvalid",
				"apply requires exactly one -f input or the demo shortcut",
				format,
				stderr,
			)
		}
		if profileID != "" || modelID != "" || contractID != "" ||
			resourceAlias != "" || fidelity != "" ||
			nodes != -1 || accelerators != -1 || healthy != -1 ||
			acceptProvisional {
			return writeFailure(
				"apply",
				"InvocationInvalid",
				"file input and demo shortcut flags are mutually exclusive",
				format,
				stderr,
			)
		}
		var encoded []byte
		encoded, err = readScenario(file, stdin)
		if err == nil {
			input, err = scenario.Document(encoded)
		}
	}
	if err != nil {
		return writeFailure("apply", "ScenarioInvalid", err.Error(), format, stderr)
	}
	compiled, receipt, err := scenario.Compile(input, snapshot)
	if err != nil {
		return writeFailure("apply", "ScenarioInvalid", err.Error(), format, stderr)
	}
	result, err := compileResult(compiled, receipt)
	if err != nil {
		return writeFailure("apply", "ScenarioInvalid", err.Error(), format, stderr)
	}
	if dryRun == "client" {
		return writeSuccess(
			presentation.Success("ScenarioCompile", "apply", result),
			format,
			stdout,
			stderr,
		)
	}
	instanceUID, generation, err := revisionPreconditions(
		instanceUIDValue,
		expectedGeneration,
	)
	if err != nil {
		return writeFailure("apply", "InvocationInvalid", err.Error(), format, stderr)
	}
	intent, err := application.NewRevisionIntent(
		compiled,
		receipt,
		creationIdentity,
		instanceUID,
		generation,
	)
	if err != nil {
		return writeFailure("apply", "ScenarioInvalid", err.Error(), format, stderr)
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: connect,
		Catalog: snapshot,
	})
	if err != nil {
		return writeFailure("apply", "InvocationInvalid", err.Error(), format, stderr)
	}
	mode := application.DryRunNone
	if dryRun == "server" {
		mode = application.DryRunServer
	}
	lifecycle, lifecycleErr := runtime.Apply(
		context.Background(),
		application.ApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: kubeconfigPath,
				ContextName:    contextName,
			},
			Intent:  intent,
			Mode:    mode,
			Async:   async,
			Timeout: timeout,
		},
	)
	return writeLifecycle(
		"apply",
		lifecycle,
		lifecycleErr,
		format,
		stdout,
		stderr,
	)
}

type connectedFlags struct {
	kubeconfigPath string
	contextName    string
	instanceUID    string
	generation     uint64
	timeout        time.Duration
	async          bool
}

func addConnectedFlags(flags *flag.FlagSet, values *connectedFlags, mutation bool) {
	flags.StringVar(
		&values.kubeconfigPath,
		"kubeconfig",
		"",
		"explicit kubeconfig path",
	)
	flags.StringVar(
		&values.contextName,
		"context",
		"",
		"exact kubeconfig context",
	)
	flags.DurationVar(
		&values.timeout,
		"timeout",
		5*time.Minute,
		"terminal wait timeout",
	)
	if mutation {
		flags.StringVar(
			&values.instanceUID,
			"instance-uid",
			"",
			"exact existing instance UID",
		)
		flags.Uint64Var(
			&values.generation,
			"expected-generation",
			0,
			"exact existing desired generation",
		)
		flags.BoolVar(
			&values.async,
			"async",
			false,
			"return after revision acceptance",
		)
	}
}

func runStatus(
	args []string,
	snapshot catalog.Snapshot,
	connect application.ConnectorFunc,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	nameValue, flagArgs, ok := exactNameArgument(args)
	if !ok {
		return writeFailure(
			"status",
			"InvocationInvalid",
			"usage: kasim status <instance-name> --kubeconfig PATH --context NAME",
			format,
			stderr,
		)
	}
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var output string
	var watch bool
	var connected connectedFlags
	flags.StringVar(&output, "o", "human", "human, json, or yaml")
	flags.StringVar(&output, "output", "human", "human, json, or yaml")
	flags.BoolVar(&watch, "watch", false, "watch until the current revision is Ready")
	addConnectedFlags(flags, &connected, false)
	if err := flags.Parse(flagArgs); err != nil || flags.NArg() != 0 {
		message := "status accepts one exact instance name and flags"
		if err != nil {
			message = err.Error()
		}
		return writeFailure("status", "InvocationInvalid", message, format, stderr)
	}
	name, err := domain.ParseName(nameValue)
	if err != nil {
		return writeFailure("status", "InvocationInvalid", err.Error(), format, stderr)
	}
	if err := validateConnectedFlags(connected, false); err != nil {
		return writeFailure("status", "InvocationInvalid", err.Error(), format, stderr)
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: connect,
		Catalog: snapshot,
	})
	if err != nil {
		return writeFailure("status", "InvocationInvalid", err.Error(), format, stderr)
	}
	result, observeErr := runtime.Observe(
		context.Background(),
		application.ObserveRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: connected.kubeconfigPath,
				ContextName:    connected.contextName,
			},
			Name:    name,
			Watch:   watch,
			Timeout: connected.timeout,
		},
	)
	return writeLifecycle(
		"status",
		result,
		observeErr,
		format,
		stdout,
		stderr,
	)
}

func runHealth(
	args []string,
	snapshot catalog.Snapshot,
	connect application.ConnectorFunc,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	nameValue, flagArgs, ok := exactNameArgument(args)
	if !ok {
		return writeFailure(
			"health",
			"InvocationInvalid",
			"usage: kasim health <instance-name> --group NAME --pool NAME --healthy COUNT",
			format,
			stderr,
		)
	}
	flags := flag.NewFlagSet("health", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var output, group, pool string
	var healthy int64
	healthy = -1
	var connected connectedFlags
	flags.StringVar(&output, "o", "human", "human, json, or yaml")
	flags.StringVar(&output, "output", "human", "human, json, or yaml")
	flags.StringVar(&group, "group", "", "exact Node Group")
	flags.StringVar(&pool, "pool", "", "exact Accelerator Pool")
	flags.Int64Var(&healthy, "healthy", -1, "healthy accelerators per synthetic Node")
	addConnectedFlags(flags, &connected, true)
	if err := flags.Parse(flagArgs); err != nil || flags.NArg() != 0 {
		message := "health accepts one exact instance name and flags"
		if err != nil {
			message = err.Error()
		}
		return writeFailure("health", "InvocationInvalid", message, format, stderr)
	}
	if group == "" || pool == "" || healthy < 0 {
		return writeFailure(
			"health",
			"InvocationInvalid",
			"health requires --group, --pool, and non-negative --healthy",
			format,
			stderr,
		)
	}
	change, err := scenario.Health(group, pool, healthy)
	if err != nil {
		return writeFailure("health", "ScenarioInvalid", err.Error(), format, stderr)
	}
	return runTypedRevision(
		"health",
		nameValue,
		change,
		connected,
		snapshot,
		connect,
		format,
		stdout,
		stderr,
	)
}

func runScale(
	args []string,
	snapshot catalog.Snapshot,
	connect application.ConnectorFunc,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	nameValue, flagArgs, ok := exactNameArgument(args)
	if !ok {
		return writeFailure(
			"scale",
			"InvocationInvalid",
			"usage: kasim scale <instance-name> --group NAME --replicas COUNT",
			format,
			stderr,
		)
	}
	flags := flag.NewFlagSet("scale", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var output, group string
	var replicas int64
	replicas = -1
	var connected connectedFlags
	flags.StringVar(&output, "o", "human", "human, json, or yaml")
	flags.StringVar(&output, "output", "human", "human, json, or yaml")
	flags.StringVar(&group, "group", "", "exact Node Group")
	flags.Int64Var(&replicas, "replicas", -1, "desired synthetic Node replicas")
	addConnectedFlags(flags, &connected, true)
	if err := flags.Parse(flagArgs); err != nil || flags.NArg() != 0 {
		message := "scale accepts one exact instance name and flags"
		if err != nil {
			message = err.Error()
		}
		return writeFailure("scale", "InvocationInvalid", message, format, stderr)
	}
	if group == "" || replicas < 0 {
		return writeFailure(
			"scale",
			"InvocationInvalid",
			"scale requires --group and non-negative --replicas",
			format,
			stderr,
		)
	}
	change, err := scenario.Scale(group, replicas)
	if err != nil {
		return writeFailure("scale", "ScenarioInvalid", err.Error(), format, stderr)
	}
	return runTypedRevision(
		"scale",
		nameValue,
		change,
		connected,
		snapshot,
		connect,
		format,
		stdout,
		stderr,
	)
}

func runTypedRevision(
	command, nameValue string,
	change scenario.TypedRevisionChange,
	connected connectedFlags,
	snapshot catalog.Snapshot,
	connect application.ConnectorFunc,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	name, err := domain.ParseName(nameValue)
	if err != nil {
		return writeFailure(command, "InvocationInvalid", err.Error(), format, stderr)
	}
	if err := validateConnectedFlags(connected, true); err != nil {
		return writeFailure(command, "InvocationInvalid", err.Error(), format, stderr)
	}
	instanceUID, generation, err := revisionPreconditions(
		connected.instanceUID,
		connected.generation,
	)
	if err != nil {
		return writeFailure(command, "InvocationInvalid", err.Error(), format, stderr)
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: connect,
		Catalog: snapshot,
	})
	if err != nil {
		return writeFailure(command, "InvocationInvalid", err.Error(), format, stderr)
	}
	result, applyErr := runtime.Apply(
		context.Background(),
		application.ApplyRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: connected.kubeconfigPath,
				ContextName:    connected.contextName,
			},
			TypedRevision: &application.TypedRevisionRequest{
				Name:               name,
				InstanceUID:        instanceUID,
				ExpectedGeneration: generation,
				Change:             change,
			},
			Async:   connected.async,
			Timeout: connected.timeout,
		},
	)
	return writeLifecycle(
		command,
		result,
		applyErr,
		format,
		stdout,
		stderr,
	)
}

func runDelete(
	args []string,
	snapshot catalog.Snapshot,
	connect application.ConnectorFunc,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	nameValue, flagArgs, ok := exactNameArgument(args)
	if !ok {
		return writeFailure(
			"delete",
			"InvocationInvalid",
			"usage: kasim delete <instance-name> --instance-uid UID --expected-generation N",
			format,
			stderr,
		)
	}
	flags := flag.NewFlagSet("delete", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var output string
	var connected connectedFlags
	flags.StringVar(&output, "o", "human", "human, json, or yaml")
	flags.StringVar(&output, "output", "human", "human, json, or yaml")
	addConnectedFlags(flags, &connected, true)
	if err := flags.Parse(flagArgs); err != nil || flags.NArg() != 0 {
		message := "delete accepts one exact instance name and safety flags"
		if err != nil {
			message = err.Error()
		}
		return writeFailure("delete", "InvocationInvalid", message, format, stderr)
	}
	name, err := domain.ParseName(nameValue)
	if err != nil {
		return writeFailure("delete", "InvocationInvalid", err.Error(), format, stderr)
	}
	if err := validateConnectedFlags(connected, true); err != nil {
		return writeFailure("delete", "InvocationInvalid", err.Error(), format, stderr)
	}
	instanceUID, generation, err := revisionPreconditions(
		connected.instanceUID,
		connected.generation,
	)
	if err != nil {
		return writeFailure("delete", "InvocationInvalid", err.Error(), format, stderr)
	}
	runtime, err := application.NewScenarioRuntime(application.RuntimeOptions{
		Connect: connect,
		Catalog: snapshot,
	})
	if err != nil {
		return writeFailure("delete", "InvocationInvalid", err.Error(), format, stderr)
	}
	result, deleteErr := runtime.Delete(
		context.Background(),
		application.DeleteRequest{
			Selection: cluster.TargetSelection{
				KubeconfigPath: connected.kubeconfigPath,
				ContextName:    connected.contextName,
			},
			Name:               name,
			InstanceUID:        instanceUID,
			ExpectedGeneration: generation,
			Async:              connected.async,
			Timeout:            connected.timeout,
		},
	)
	return writeLifecycle(
		"delete",
		result,
		deleteErr,
		format,
		stdout,
		stderr,
	)
}

func exactNameArgument(args []string) (string, []string, bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, false
	}
	return args[0], args[1:], true
}

func validateConnectedFlags(values connectedFlags, mutation bool) error {
	if values.kubeconfigPath == "" || values.contextName == "" {
		return fmt.Errorf("connected command requires --kubeconfig and --context")
	}
	if values.timeout <= 0 {
		return fmt.Errorf("--timeout must be positive")
	}
	if mutation && (values.instanceUID == "" || values.generation == 0) {
		return fmt.Errorf(
			"mutation requires --instance-uid and positive --expected-generation",
		)
	}
	if values.generation > math.MaxInt64 {
		return fmt.Errorf("--expected-generation is too large")
	}
	return nil
}

func revisionPreconditions(
	instanceUIDValue string,
	generationValue uint64,
) (domain.InstanceUID, domain.Generation, error) {
	generation, err := domain.NewGeneration(int64(generationValue))
	if err != nil {
		return domain.InstanceUID{}, domain.Generation{}, err
	}
	if instanceUIDValue == "" {
		return domain.InstanceUID{}, generation, nil
	}
	instanceUID, err := domain.ParseInstanceUID(instanceUIDValue)
	if err != nil {
		return domain.InstanceUID{}, domain.Generation{}, err
	}
	return instanceUID, generation, nil
}

func requestedOutputFormat(args []string) (presentation.OutputFormat, error) {
	name := "human"
	for index := 0; index < len(args); index++ {
		switch {
		case args[index] == "-o" || args[index] == "--output":
			if index+1 >= len(args) {
				return presentation.OutputFormat{}, fmt.Errorf("%s requires a value", args[index])
			}
			name = args[index+1]
			index++
		case strings.HasPrefix(args[index], "-o="):
			name = strings.TrimPrefix(args[index], "-o=")
		case strings.HasPrefix(args[index], "--output="):
			name = strings.TrimPrefix(args[index], "--output=")
		}
	}
	return presentation.ParseOutputFormat(name)
}

func outputFlagSet(name string) (*flag.FlagSet, *string) {
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var output string
	flags.StringVar(&output, "o", "human", "human, json, or yaml")
	flags.StringVar(&output, "output", "human", "human, json, or yaml")
	return flags, &output
}

func readScenario(file string, stdin io.Reader) ([]byte, error) {
	if file == "-" {
		encoded, err := io.ReadAll(io.LimitReader(stdin, maximumScenarioBytes+1))
		if err != nil {
			return nil, fmt.Errorf("read Scenario stdin: %w", err)
		}
		if len(encoded) > maximumScenarioBytes {
			return nil, fmt.Errorf("Scenario input exceeds %d bytes", maximumScenarioBytes)
		}
		return encoded, nil
	}
	if strings.Contains(file, "://") {
		return nil, fmt.Errorf("-f accepts one local file or - for stdin")
	}
	info, err := os.Stat(file)
	if err != nil {
		return nil, fmt.Errorf("inspect Scenario file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("Scenario path must be one regular local file")
	}
	if info.Size() > maximumScenarioBytes {
		return nil, fmt.Errorf("Scenario input exceeds %d bytes", maximumScenarioBytes)
	}
	encoded, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("read Scenario file: %w", err)
	}
	return encoded, nil
}

func profileSummaryResult(profile catalog.ProfileSummary) presentation.ProfileSummaryResult {
	return presentation.ProfileSummaryResult{
		ID:          profile.ID(),
		DisplayName: profile.DisplayName(),
		Class:       profile.Class(),
		Revision:    profile.Revision(),
		Digest:      profile.Digest().String(),
	}
}

func profileResult(profile catalog.ProfileView) presentation.ProfileResult {
	evidence := evidenceResults(profile.Evidence())
	contracts := make([]presentation.ContractResult, 0, len(profile.Contracts()))
	for _, contract := range profile.Contracts() {
		resources := make([]presentation.ResourceResult, 0, len(contract.Resources()))
		for _, resource := range contract.Resources() {
			resources = append(resources, presentation.ResourceResult{
				Alias: resource.Alias(),
				Name:  resource.Name(),
				Unit:  resource.Unit(),
			})
		}
		signals := make([]presentation.IdentitySignalResult, 0, len(contract.IdentitySignals()))
		for _, signal := range contract.IdentitySignals() {
			signals = append(signals, presentation.IdentitySignalResult{
				Kind: signal.Kind(),
				Key:  signal.Key(),
			})
		}
		contracts = append(contracts, presentation.ContractResult{
			ID:              contract.ID(),
			Kind:            contract.Kind(),
			ProviderScope:   contract.ProviderScope(),
			FidelityModes:   contract.FidelityModes(),
			Resources:       resources,
			IdentitySignals: signals,
			Capabilities:    contract.Capabilities(),
			EvidenceRefs:    contract.EvidenceRefs(),
		})
	}
	models := make([]presentation.ModelResult, 0, len(profile.Models()))
	for _, model := range profile.Models() {
		models = append(models, presentation.ModelResult{
			ID:              model.ID(),
			DisplayName:     model.DisplayName(),
			Aliases:         model.Aliases(),
			Lifecycle:       model.Lifecycle(),
			Selectable:      model.Selectable(),
			Contracts:       model.Contracts(),
			ResourceAliases: model.ResourceAliases(),
			EvidenceRefs:    model.EvidenceRefs(),
		})
	}
	return presentation.ProfileResult{
		ProfileSummaryResult: presentation.ProfileSummaryResult{
			ID:          profile.ID(),
			DisplayName: profile.DisplayName(),
			Class:       profile.Class(),
			Revision:    profile.Revision(),
			Digest:      profile.Digest().String(),
		},
		Evidence:  evidence,
		Contracts: contracts,
		Models:    models,
	}
}

func compileResult(
	compiled scenario.CanonicalScenario,
	receipt scenario.CompileReceipt,
) (presentation.ScenarioCompileResult, error) {
	var canonical any
	if err := json.Unmarshal(compiled.Bytes(), &canonical); err != nil {
		return presentation.ScenarioCompileResult{}, fmt.Errorf(
			"decode canonical Scenario for presentation: %w",
			err,
		)
	}
	resolutions := make([]presentation.ResolutionResult, 0, len(receipt.Resolutions()))
	for _, resolution := range receipt.Resolutions() {
		resolutions = append(resolutions, presentation.ResolutionResult{
			ProfileClass:  resolution.ProfileClass(),
			ProfileDigest: resolution.ProfileDigest().String(),
			ModelID:       resolution.ModelID(),
			ContractID:    resolution.ContractID(),
			ResourceAlias: resolution.ResourceAlias(),
			ResourceName:  resolution.ResourceName(),
			Evidence:      evidenceResults(resolution.Evidence()),
		})
	}
	return presentation.ScenarioCompileResult{
		ScenarioName:      compiled.Scenario().Name().String(),
		ScenarioDigest:    compiled.Digest().String(),
		CatalogDigest:     receipt.CatalogDigest().String(),
		Resolutions:       resolutions,
		CanonicalScenario: canonical,
	}, nil
}

func evidenceResults(evidence []catalog.EvidenceReceipt) []presentation.EvidenceResult {
	results := make([]presentation.EvidenceResult, 0, len(evidence))
	for _, receipt := range evidence {
		results = append(results, presentation.EvidenceResult{
			ID:        receipt.ID,
			Grade:     receipt.Grade,
			Source:    receipt.Source,
			Revision:  receipt.Revision,
			CheckedAt: receipt.CheckedAt,
		})
	}
	return results
}

func writeLifecycle(
	command string,
	result application.LifecycleResult,
	lifecycleErr error,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	presented := lifecycleResult(result)
	if lifecycleErr == nil {
		return writeSuccess(
			presentation.Success("ScenarioLifecycle", command, presented),
			format,
			stdout,
			stderr,
		)
	}
	diagnostic := application.DiagnosticForError(lifecycleErr)
	envelope := presentation.Failure(command, diagnostic)
	if result.Receipt.InstanceName().String() != "" {
		envelope = presentation.FailureWithResult(command, diagnostic, presented)
	}
	encoded, err := presentation.Render(envelope, format)
	if err != nil {
		_, _ = io.WriteString(
			stderr,
			"Error [InvocationInvalid]: command failed safely\n",
		)
		return 2
	}
	_, _ = stderr.Write(encoded)
	return diagnostic.ExitCategory().Code()
}

func lifecycleResult(
	result application.LifecycleResult,
) presentation.LifecycleResult {
	profileDigests := result.Receipt.ProfileDigests()
	presentedDigests := make([]string, 0, len(profileDigests))
	for _, digest := range profileDigests {
		presentedDigests = append(presentedDigests, digest.String())
	}
	presented := presentation.LifecycleResult{
		Connection: presentation.ConnectionResult{
			ContextName:       result.Connection.ContextName,
			APIServerURL:      result.Connection.APIServerURL,
			TargetFingerprint: result.Connection.TargetFingerprint.String(),
			CADigest:          result.Connection.CADigest.String(),
		},
		Receipt: presentation.RevisionReceiptResult{
			InstanceName:       result.Receipt.InstanceName().String(),
			InstanceUID:        result.Receipt.InstanceUID().String(),
			DesiredGeneration:  result.Receipt.DesiredGeneration().Value(),
			ObservedGeneration: result.Receipt.ObservedGeneration().Value(),
			RevisionDigest:     result.Receipt.RevisionDigest().String(),
			ProfileDigests:     presentedDigests,
			RevisionAccepted:   result.Receipt.RevisionAccepted(),
			NoOp:               result.Receipt.NoOp(),
		},
		Warning: result.Warning,
	}
	if result.Snapshot != nil {
		snapshot := result.Snapshot
		profiles := make(
			[]presentation.ProfileReceiptResult,
			0,
			len(snapshot.Profiles),
		)
		for _, profile := range snapshot.Profiles {
			profiles = append(profiles, presentation.ProfileReceiptResult{
				ID:       profile.ID,
				Revision: profile.Revision,
				Digest:   profile.Digest.String(),
				Class:    profile.Class,
			})
		}
		pools := make([]presentation.PoolResult, 0, len(snapshot.Pools))
		for _, pool := range snapshot.Pools {
			pools = append(pools, presentation.PoolResult{
				Group:            pool.Group,
				Pool:             pool.Pool,
				RequestedTotal:   pool.RequestedTotal,
				RequestedHealthy: pool.RequestedHealthy,
				ObservedTotal:    pool.ObservedTotal,
				ObservedHealthy:  pool.ObservedHealthy,
			})
		}
		inventory := make(
			[]presentation.InventoryResult,
			0,
			len(snapshot.Inventory),
		)
		for _, item := range snapshot.Inventory {
			inventory = append(inventory, presentation.InventoryResult{
				APIVersion: item.APIVersion,
				Kind:       item.Kind,
				Count:      item.Count,
			})
		}
		fidelity := make(
			[]presentation.FidelitySurfaceResult,
			0,
			len(snapshot.Fidelity),
		)
		for _, surface := range snapshot.Fidelity {
			fidelity = append(
				fidelity,
				presentation.FidelitySurfaceResult{
					Surface: surface.Surface,
					State:   surface.State,
				},
			)
		}
		diagnostics := make(
			[]presentation.SnapshotDiagnosticResult,
			0,
			len(snapshot.Diagnostics),
		)
		for _, diagnostic := range snapshot.Diagnostics {
			diagnostics = append(
				diagnostics,
				presentation.SnapshotDiagnosticResult{
					Code:             diagnostic.Code,
					Message:          diagnostic.Message,
					Retryable:        diagnostic.Retryable,
					RevisionAccepted: diagnostic.RevisionAccepted,
					ExitCategory:     diagnostic.ExitCategory,
				},
			)
		}
		conditions := make(
			[]presentation.ConditionResult,
			0,
			len(snapshot.Conditions),
		)
		for _, condition := range snapshot.Conditions {
			transitionTime := ""
			if !condition.LastTransitionTime.IsZero() {
				transitionTime = condition.LastTransitionTime.
					UTC().
					Format(time.RFC3339Nano)
			}
			conditions = append(conditions, presentation.ConditionResult{
				Type:               condition.Type,
				Status:             condition.Status,
				Reason:             condition.Reason,
				Message:            condition.Message,
				ObservedGeneration: condition.ObservedGeneration,
				LastTransitionTime: transitionTime,
			})
		}
		presented.Snapshot = &presentation.SnapshotResult{
			InstanceName:         snapshot.InstanceName.String(),
			InstanceUID:          snapshot.InstanceUID.String(),
			TargetFingerprint:    snapshot.TargetFingerprint.String(),
			DesiredGeneration:    snapshot.DesiredGeneration.Value(),
			ObservedGeneration:   snapshot.ObservedGeneration.Value(),
			RevisionDigest:       snapshot.RevisionDigest.String(),
			Profiles:             profiles,
			Phase:                snapshot.Phase,
			Pools:                pools,
			PoolsTruncated:       snapshot.PoolsTruncated,
			Inventory:            inventory,
			InventoryTruncated:   snapshot.InventoryTruncated,
			Fidelity:             fidelity,
			FidelityTruncated:    snapshot.FidelityTruncated,
			Diagnostics:          diagnostics,
			DiagnosticsTruncated: snapshot.DiagnosticsTruncated,
			Conditions:           conditions,
			ConditionsTruncated:  snapshot.ConditionsTruncated,
		}
	}
	return presented
}

func writeSuccess(
	envelope presentation.OutputEnvelope,
	format presentation.OutputFormat,
	stdout, stderr io.Writer,
) int {
	encoded, err := presentation.Render(envelope, format)
	if err != nil {
		return writeFailure(
			envelope.Command,
			"InvocationInvalid",
			err.Error(),
			format,
			stderr,
		)
	}
	if _, err := stdout.Write(encoded); err != nil {
		return writeFailure(
			envelope.Command,
			"InvocationInvalid",
			"write command output failed",
			format,
			stderr,
		)
	}
	return 0
}

func writeFailure(
	command, codeName, message string,
	format presentation.OutputFormat,
	stderr io.Writer,
) int {
	code, codeErr := domain.ParseDiagnosticCode(codeName)
	exitCategory, categoryErr := domain.ParseExitCategory(2)
	if len(message) > domain.MaximumDiagnosticMessageBytes {
		message = message[:domain.MaximumDiagnosticMessageBytes]
	}
	diagnostic, diagnosticErr := domain.NewDiagnostic(
		code,
		message,
		false,
		false,
		exitCategory,
	)
	if codeErr != nil || categoryErr != nil || diagnosticErr != nil {
		_, _ = io.WriteString(stderr, "Error [InvocationInvalid]: command failed safely\n")
		return 2
	}
	encoded, err := presentation.Render(presentation.Failure(command, diagnostic), format)
	if err != nil {
		_, _ = io.WriteString(stderr, "Error [InvocationInvalid]: command failed safely\n")
		return 2
	}
	_, _ = stderr.Write(encoded)
	return 2
}
