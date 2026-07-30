// Package cli delivers the concrete kasim command surface without introducing
// a plugin seam or reading implicit Kubernetes configuration.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/LinkMaq/kube-accelerator-sim/internal/catalog"
	"github.com/LinkMaq/kube-accelerator-sim/internal/domain"
	"github.com/LinkMaq/kube-accelerator-sim/internal/presentation"
	"github.com/LinkMaq/kube-accelerator-sim/internal/scenario"
	"github.com/LinkMaq/kube-accelerator-sim/internal/version"
)

const maximumScenarioBytes = 1 << 20

// Run executes one concrete CLI invocation and returns its stable exit
// category. Successful envelopes go to stdout; failures go to stderr.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
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

	snapshot, err := catalog.LoadBundled()
	if err != nil {
		return writeFailure(
			args[0],
			"CatalogInvalid",
			err.Error(),
			format,
			stderr,
		)
	}
	switch args[0] {
	case "version":
		return runVersion(args[1:], snapshot, format, stdout, stderr)
	case "profile":
		return runProfile(args[1:], snapshot, format, stdout, stderr)
	case "apply":
		return runApply(args[1:], stdin, snapshot, format, stdout, stderr)
	case "status", "health", "scale", "delete":
		return writeFailure(
			args[0],
			"InvocationInvalid",
			fmt.Sprintf("%s requires the cluster runtime, which is not available in this build stage", args[0]),
			format,
			stderr,
		)
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
	var profileID, modelID, contractID, resourceAlias, fidelity string
	var nodes, accelerators, healthy int64
	var acceptProvisional bool
	flags.StringVar(&file, "f", "", "local Scenario file or - for stdin")
	flags.StringVar(&dryRun, "dry-run", "", "client")
	flags.StringVar(&output, "o", "human", "human, json, or yaml")
	flags.StringVar(&output, "output", "human", "human, json, or yaml")
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
	if dryRun != "client" {
		return writeFailure(
			"apply",
			"InvocationInvalid",
			"offline apply requires --dry-run=client",
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
	return writeSuccess(
		presentation.Success("ScenarioCompile", "apply", result),
		format,
		stdout,
		stderr,
	)
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
