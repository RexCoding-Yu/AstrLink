package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/QuantumNous/astrlink/core/contract"
	"github.com/QuantumNous/astrlink/core/internal/accesstoken"
	"github.com/QuantumNous/astrlink/core/internal/accountauth"
	"github.com/QuantumNous/astrlink/core/internal/buildinfo"
	"github.com/QuantumNous/astrlink/core/internal/codingplan"
	"github.com/QuantumNous/astrlink/core/internal/controlapi"
	"github.com/QuantumNous/astrlink/core/internal/coreapp"
	"github.com/QuantumNous/astrlink/core/internal/endpoint"
	"github.com/QuantumNous/astrlink/core/internal/ingress"
	"github.com/QuantumNous/astrlink/core/internal/localkey"
	"github.com/QuantumNous/astrlink/core/internal/networkproxy"
	"github.com/QuantumNous/astrlink/core/internal/parentwatch"
	"github.com/QuantumNous/astrlink/core/internal/pricing"
	"github.com/QuantumNous/astrlink/core/internal/privacy"
	"github.com/QuantumNous/astrlink/core/internal/privacymodel"
	"github.com/QuantumNous/astrlink/core/internal/privacyworker"
	"github.com/QuantumNous/astrlink/core/internal/relaykitbridge"
	"github.com/QuantumNous/astrlink/core/internal/servicemodel"
	"github.com/QuantumNous/astrlink/core/internal/servicetest"
	"github.com/QuantumNous/astrlink/core/internal/storage/sqlite"
	"github.com/QuantumNous/astrlink/core/internal/subscription"
)

func main() {
	if len(os.Args) > 1 {
		commandCtx, stopCommand := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		code, handled := runOfflineCommand(commandCtx, os.Args[1:], os.Stdin, os.Stdout, os.Stderr)
		stopCommand()
		if handled {
			os.Exit(code)
		}
	}
	config := coreapp.DefaultConfig(buildinfo.Version, buildinfo.Commit)
	parentPID := 0
	dataDirectory := ""
	privacyWorkerPath := ""
	classifierWorkerPath := ""
	controlTokenStdin := false
	observerTokenStdin := false
	localKeyStdin := false
	localKeyFile := ""
	outboundProxy := "environment"
	maxConcurrentInspections := ingress.DefaultMaxConcurrentInspections
	var maxRequestBodyMiB uint64
	responseStartTimeoutSeconds := ingress.DefaultResponseStartTimeoutSeconds
	flag.StringVar(&config.InferenceListen, "inference-listen", config.InferenceListen, "loopback inference listen address")
	flag.BoolVar(&config.InferencePortFallback, "inference-port-fallback", false, "use an ephemeral loopback port when the inference port is occupied")
	flag.StringVar(&config.ControlListen, "control-listen", config.ControlListen, "loopback control listen address")
	flag.IntVar(&parentPID, "parent-pid", 0, "optional desktop parent PID to watch on Unix")
	flag.StringVar(&dataDirectory, "data-dir", "", "optional persistent application data directory")
	flag.StringVar(&privacyWorkerPath, "privacy-worker", "", "optional bundled privacy worker executable")
	flag.StringVar(&classifierWorkerPath, "classifier-worker", "", "optional bundled classifier worker executable")
	flag.BoolVar(&controlTokenStdin, "control-token-stdin", false, "read the per-start control token from stdin")
	flag.BoolVar(&observerTokenStdin, "observer-token-stdin", false, "read the per-start observer token from the third stdin line")
	flag.BoolVar(&localKeyStdin, "kek-stdin", false, "read the local key as 64 hex digits from the second stdin line")
	flag.StringVar(&localKeyFile, "kek-file", "", "local key file path; defaults to $"+localkey.EnvKeyFile+", then <data-dir>/"+localkey.FileName)
	flag.IntVar(&maxConcurrentInspections, "max-concurrent-inspections", maxConcurrentInspections, "maximum requests that may parse and classify at once")
	flag.Uint64Var(&maxRequestBodyMiB, "max-request-body-mib", 0, "maximum inference request body size in MiB; 0 means unlimited")
	flag.IntVar(&responseStartTimeoutSeconds, "response-start-timeout-seconds", responseStartTimeoutSeconds, "seconds to wait for upstream response headers before failing over; 0 waits indefinitely")
	flag.StringVar(&outboundProxy, "outbound-proxy", outboundProxy, "outbound proxy mode: environment, system, or direct")
	flag.CommandLine.SetOutput(os.Stderr)
	flag.Parse()

	logger := log.New(os.Stderr, "astrlink-core: ", log.LstdFlags)
	if err := ingress.ValidateMaxConcurrentInspections(maxConcurrentInspections); err != nil {
		logger.Printf("%v", err)
		os.Exit(2)
	}
	if err := ingress.ValidateResponseStartTimeoutSeconds(responseStartTimeoutSeconds); err != nil {
		logger.Printf("%v", err)
		os.Exit(2)
	}
	if maxRequestBodyMiB > 1<<32-1 {
		logger.Printf("max-request-body-mib must be at most 4294967295 (0 means unlimited)")
		os.Exit(2)
	}
	proxy, err := networkproxy.New(outboundProxy)
	if err != nil {
		logger.Printf("%v", err)
		os.Exit(2)
	}
	// Configure once, before any clients or transport clones are created.
	// OAuth, subscriptions, discovery, downloads and inference share this policy.
	outboundTransport := http.DefaultTransport.(*http.Transport).Clone()
	outboundTransport.Proxy = proxy
	http.DefaultTransport = networkproxy.WrapTransport(outboundTransport)

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	ctx, stopParentWatch, err := parentwatch.NotifyContext(signalCtx, parentPID, time.Second)
	if err != nil {
		logger.Printf("parent watchdog: %v", err)
		os.Exit(2)
	}
	defer stopParentWatch()

	dependencies := coreapp.Dependencies{
		InferenceHandler: ingress.NewWithDependencies(ingress.Dependencies{
			MaxRequestBodyMiB: uint32(maxRequestBodyMiB),
		}),
	}
	var closeStore func() error
	if (localKeyStdin || localKeyFile != "") && !controlTokenStdin {
		logger.Printf("--kek-stdin and --kek-file require persistent mode")
		os.Exit(2)
	}
	if dataDirectory != "" || controlTokenStdin {
		if dataDirectory == "" || !controlTokenStdin {
			logger.Printf("persistent mode requires both --data-dir and --control-token-stdin")
			os.Exit(2)
		}
		tokens, err := readStdinTokens(os.Stdin, stdinLineCount(observerTokenStdin, localKeyStdin), localKeyStdin)
		if err != nil {
			logger.Printf("read local control tokens: %v", err)
			os.Exit(2)
		}
		controlToken := tokens.control
		if localKeyFile == "" {
			localKeyFile = os.Getenv(localkey.EnvKeyFile)
		}
		localKey, _, err := localkey.Resolve(localkey.Options{
			StdinKey: tokens.localKey,
			KeyFile:  localKeyFile,
			DataDir:  dataDirectory,
			Logf:     logger.Printf,
		})
		clear(tokens.localKey)
		if err != nil {
			logger.Printf("load local key: %v", err)
			os.Exit(1)
		}
		store, err := sqlite.Open(ctx, filepath.Join(dataDirectory, "astrlink.db"),
			sqlite.WithLocalKey(localKey), sqlite.WithLogger(logger.Printf))
		clear(localKey)
		if err != nil {
			logger.Printf("open persistent store: %v", err)
			os.Exit(1)
		}
		if runtime.GOOS != "windows" {
			config.ControlSocketPath = filepath.Join(dataDirectory, "control.sock")
		}
		closeStore = store.Close
		if recovered, recoverErr := store.RecoverPendingRequestRecords(ctx); recoverErr != nil {
			logger.Printf("recover interrupted request records: %v", recoverErr)
		} else if recovered > 0 {
			logger.Printf("recovered %d interrupted request record(s)", recovered)
		}
		rawVault := controlapi.NewRawVault(store, controlapi.RawVaultOptions{Logf: logger.Printf})
		warnWithoutRawPassword(ctx, rawVault, dataDirectory, logger.Printf)
		accessTokenManager, err := accesstoken.NewManager(store)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure persistent access tokens: %v", err)
			os.Exit(1)
		}
		privacyModel, err := privacymodel.NewRegistry(ctx, privacymodel.RegistryConfig{
			RootDirectory: filepath.Join(dataDirectory, "privacy-model"),
			Store:         store,
			Logf:          logger.Printf,
		})
		if err != nil {
			_ = store.Close()
			logger.Printf("configure local privacy model: %v", err)
			os.Exit(1)
		}
		if privacyWorkerPath == "" {
			privacyWorkerPath, err = privacyworker.SiblingExecutablePath()
			if err != nil {
				_ = store.Close()
				logger.Printf("locate local privacy worker: %v", err)
				os.Exit(1)
			}
		}
		privacyWorker, err := privacyworker.New(privacyworker.Config{
			ExecutablePath: privacyWorkerPath,
			Model:          privacyModel,
		})
		if err != nil {
			_ = store.Close()
			logger.Printf("configure local privacy worker: %v", err)
			os.Exit(1)
		}
		defer privacyWorker.Close()
		policyRecord, err := store.GetPolicy(ctx, contract.DefaultPrivacyPolicyID)
		if err != nil {
			_ = store.Close()
			logger.Printf("load local privacy policy: %v", err)
			os.Exit(1)
		}
		privacyWorker.ApplyPolicy(policyRecord.Policy)
		policyProvider, err := privacy.NewStorePolicyProvider(store)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure local privacy policy: %v", err)
			os.Exit(1)
		}
		privacyFilter, err := privacy.New(policyProvider, privacyWorker)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure local privacy filter: %v", err)
			os.Exit(1)
		}
		conversionEngine := relaykitbridge.NewEngine()
		// One registry serves inference, gateway-initiated requests and
		// learning, so a learned identity applies everywhere at once.
		identities := accountauth.NewIdentityRegistry(store, store)
		if err := identities.Hydrate(ctx); err != nil {
			logger.Printf("load learned client identities: %v", err)
		}
		subscriptionManager, err := newSubscriptionManager(ctx, store, identities, logger.Printf)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure subscription manager: %v", err)
			os.Exit(1)
		}
		subscriptionManager.SetRiskEventStore(store)
		pricingManager := pricing.NewManager(store, nil)
		subscriptionManager.SetUsageObservers(
			func(ctx context.Context, account contract.SubscriptionAccount, usage contract.SubscriptionUsage) error {
				current, err := store.GetService(ctx, account.ID)
				if err != nil {
					return err
				}
				if current.Service.Subscription == nil || current.Service.Subscription.ProviderAccountID != account.ProviderAccountID {
					return nil
				}
				return store.ObserveSubscriptionUsage(ctx, current.Service, usage)
			},
			func(ctx context.Context, account contract.SubscriptionAccount) error {
				current, err := store.GetService(ctx, account.ID)
				if err != nil {
					return err
				}
				if current.Service.Subscription == nil || current.Service.Subscription.ProviderAccountID != account.ProviderAccountID {
					return nil
				}
				return store.ObserveSubscriptionReset(ctx, current.Service)
			},
		)
		resolver, err := endpoint.NewStoreResolver(store)
		if err != nil {
			_ = store.Close()
			logger.Printf("configure persistent endpoint resolver: %v", err)
			os.Exit(1)
		}
		resolver.WithRuntimeProfile(contract.RuntimeProfile{RelayKitAvailable: true, Edges: conversionEngine.Edges()})
		resolver.WithSubscriptionBaseURL(subscriptionManager.APIBaseURL())
		gatewayDependencies := ingress.Dependencies{
			ProxyCredentials: store,
			Resolver:         resolver,
			Authorizer: endpoint.NewServiceAuthorizer(store, subscriptionManager, subscriptionManager.Provider().IdentityPolicy()).
				WithRoutingSettings(store).WithIdentities(identities),
			AccessTokenAuthenticator: ingress.AccessTokenAuthenticatorFunc(
				func(ctx context.Context, raw string) (contract.AccessTokenID, error) {
					return accessTokenManager.Authenticate(ctx, raw)
				},
			),
			PrivacyFilter: privacyFilter,
			PolicyWarningReporter: ingress.PolicyWarningReporterFunc(
				func(protocol contract.ProtocolID, endpointID contract.ServiceID, summary string) {
					logger.Printf(
						"privacy policy warning: protocol=%s service_id=%s findings=%s",
						protocol,
						endpointID,
						summary,
					)
				},
			),
			RequestRecords:           store,
			AuditSettings:            store,
			AuditBlobs:               store,
			RecordLogger:             logger.Printf,
			ConversionEngine:         conversionEngine,
			MaxConcurrentInspections: maxConcurrentInspections,
			MaxRequestBodyMiB:        uint32(maxRequestBodyMiB),
			ResponseStartTimeout:     time.Duration(responseStartTimeoutSeconds) * time.Second,
			SubscriptionRisk:         subscriptionRiskReporter{manager: subscriptionManager},
			Identities:               identities,
		}
		handler, err := controlapi.NewWithDependencies(config.Version, controlapi.Dependencies{
			ServiceStore: store,
			PricingStore: store, PricingManager: pricingManager,
			AccessTokenManager: accessTokenManager,
			PolicyStore:        store,
			PrivacyModels:      privacyModel,
			PrivacyFilter:      privacyFilter,
			PolicyChanged:      privacyWorker.ApplyPolicy,
			RequestRecords:     store,
			AuditSettings:      store,
			AuditKeys:          store,
			AuditBlobs:         store,
			RawVault:           rawVault,
			LocalData:          store,
			ClientIdentities:   identities,
			Subscriptions:      subscriptionManager,
			CodingPlans:        codingplan.New(store, nil),
			ServiceModels:      servicemodel.New(store, subscriptionManager, nil),
			ServiceTester:      servicetest.NewWithDependencies(gatewayDependencies, subscriptionManager.APIBaseURLFor),
			BuiltinToolTester:  ingress.NewWithDependencies(gatewayDependencies),
			ControlToken:       controlToken,
			ObserverToken:      tokens.observer,
			ConversionEngine:   conversionEngine,
			Shutdown:           stopSignals,
		})
		if err != nil {
			_ = store.Close()
			logger.Printf("configure persistent control API: %v", err)
			os.Exit(1)
		}
		dependencies.ControlHandler = handler
		dependencies.RetentionSweep = func(ctx context.Context) error {
			_, err := store.SweepExpiredAuditData(ctx)
			return err
		}
		dependencies.NewInferenceHandler = func(address string) (http.Handler, error) {
			production := gatewayDependencies
			production.AllowedHost = address
			return ingress.NewProduction(production)
		}
		monitorCtx, stopMonitors := context.WithCancel(ctx)
		var monitors sync.WaitGroup
		monitors.Add(3)
		go func() { defer monitors.Done(); pricingManager.Run(monitorCtx, logger.Printf) }()
		go func() { defer monitors.Done(); subscriptionManager.RunUsageMonitor(monitorCtx) }()
		go func() { defer monitors.Done(); rawVault.Run(monitorCtx) }()
		closeStore = func() error {
			// Zero every key an agent grant or the unlock session holds.
			handler.RevokeRawGrants()
			rawVault.Lock()
			stopMonitors()
			monitors.Wait()
			return store.Close()
		}
	}
	if closeStore != nil {
		defer closeStore()
	}

	if err := coreapp.RunWithDependencies(ctx, config, os.Stdout, dependencies); err != nil {
		logger.Printf("%v", err)
		os.Exit(1)
	}
}

// Stdin carries one secret per line, in a fixed order, so a newer desktop and
// an older Core agree on positions: line 1 is the control token, line 2 is
// reserved for the injected local key, and line 3 is the observer token.
const (
	stdinControlLine = iota
	stdinLocalKeyLine
	stdinObserverLine
	maxStdinLines
)

// maxStdinLineBytes bounds one line including its newline.
const maxStdinLineBytes = 129

// rawStatusReader is the part of the raw vault the start-up warning reads.
type rawStatusReader interface {
	Status(context.Context) (controlapi.RawVaultStatus, error)
}

// warnWithoutRawPassword says once at start that raw content is not kept:
// a headless Core has no dialog to ask for the password.
func warnWithoutRawPassword(ctx context.Context, vault rawStatusReader, dataDirectory string, logf func(string, ...any)) {
	status, err := vault.Status(ctx)
	if err != nil {
		logf("read raw sealing state: %v", err)
		return
	}
	if status.PasswordSet {
		return
	}
	logf("WARNING: no raw password is set, so raw request and response content is not recorded "+
		"(shareable content and forwarding are unaffected). Set one while Core is stopped with "+
		"`astrlink-core raw-password set --data-dir %s --password-stdin`, or through POST %s",
		dataDirectory, controlapi.RawPasswordPath)
}

type stdinTokens struct {
	control  string
	observer string
	// localKey is the decoded key from line 2; the caller clears it.
	localKey []byte
}

// stdinLineCount is how many lines the desktop promised via flags. Core never
// reads past them, because the desktop keeps stdin open for the process
// lifetime and an unpromised read would block startup.
func stdinLineCount(observerTokenStdin, localKeyStdin bool) int {
	switch {
	case observerTokenStdin:
		return stdinObserverLine + 1
	case localKeyStdin:
		return stdinLocalKeyLine + 1
	}
	return stdinControlLine + 1
}

// readStdinTokens reads the promised lines. The local key line is required
// when localKeyStdin is set and ignored otherwise.
func readStdinTokens(input io.Reader, lines int, localKeyStdin bool) (tokens stdinTokens, err error) {
	values, buffer, err := readTokenLines(input, lines)
	defer clear(buffer)
	if err != nil {
		return stdinTokens{}, err
	}
	defer func() {
		if err != nil {
			clear(tokens.localKey)
			tokens = stdinTokens{}
		}
	}()
	if tokens.control, err = validateToken(string(values[stdinControlLine])); err != nil {
		return tokens, fmt.Errorf("control token: %w", err)
	}
	if localKeyStdin {
		if lines <= stdinLocalKeyLine {
			return tokens, fmt.Errorf("local key: stdin line %d was not requested", stdinLocalKeyLine+1)
		}
		if tokens.localKey, err = localkey.Parse(values[stdinLocalKeyLine]); err != nil {
			return tokens, fmt.Errorf("local key: %w", err)
		}
	}
	if lines > stdinObserverLine && len(values[stdinObserverLine]) > 0 {
		if tokens.observer, err = validateToken(string(values[stdinObserverLine])); err != nil {
			return tokens, fmt.Errorf("observer token: %w", err)
		}
		if tokens.observer == tokens.control {
			return tokens, fmt.Errorf("observer token must differ from the control token")
		}
	}
	return tokens, nil
}

// readTokenLines reads exactly count newline-terminated lines. Only the first
// line is mandatory; a later line that is empty or absent at end of input is
// returned empty. A non-empty line cut off before its newline is an error so
// a truncated secret is never accepted. It reads one byte at a time into a
// single buffer, so nothing past the promised lines is consumed and no copy
// of a secret outlives the buffer the caller clears.
func readTokenLines(input io.Reader, count int) (values [][]byte, buffer []byte, err error) {
	if input == nil {
		return nil, nil, fmt.Errorf("stdin is unavailable")
	}
	if count < 1 || count > maxStdinLines {
		return nil, nil, fmt.Errorf("stdin line count %d is out of range", count)
	}
	// Each line holds at most maxStdinLineBytes-1 bytes, so appends never
	// reallocate and leave a stray copy behind.
	buffer = make([]byte, 0, count*maxStdinLineBytes)
	defer func() {
		if err != nil {
			clear(buffer)
			buffer = nil
		}
	}()
	values = make([][]byte, count)
	var next [1]byte
	defer clear(next[:])
	for index := range values {
		start := len(buffer)
		complete := false
		var readErr error
		for !complete && readErr == nil {
			var read int
			read, readErr = input.Read(next[:])
			if read == 0 {
				continue
			}
			switch {
			case next[0] == '\n':
				complete = true
			case len(buffer)-start == maxStdinLineBytes-1:
				readErr = errStdinLineTooLong
			default:
				buffer = append(buffer, next[0])
			}
		}
		if !complete {
			if index > 0 && len(buffer) == start && errors.Is(readErr, io.EOF) {
				break
			}
			if index == 0 {
				return nil, nil, fmt.Errorf("control token: token line is incomplete")
			}
			return nil, nil, fmt.Errorf("stdin line %d is incomplete", index+1)
		}
		values[index] = buffer[start:len(buffer):len(buffer)]
	}
	return values, buffer, nil
}

var errStdinLineTooLong = errors.New("stdin line is too long")

func validateToken(token string) (string, error) {
	if len(token) < 32 || len(token) > 128 {
		return "", fmt.Errorf("token must contain 32 to 128 characters")
	}
	for _, character := range token {
		if !((character >= 'a' && character <= 'z') ||
			(character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_') {
			return "", fmt.Errorf("token must use base64url-safe characters")
		}
	}
	return token, nil
}

func newSubscriptionManager(
	ctx context.Context,
	store *sqlite.Store,
	identities *accountauth.IdentityRegistry,
	logf func(string, ...any),
) (*subscription.Manager, error) {
	oauth := accountauth.OAuthConfig{
		ResolveProxy: networkproxy.Resolver(store, store),
		ClientID:     accountauth.DefaultCodexOAuthClientID,
		Identities:   identities,
	}
	if clientID := strings.TrimSpace(os.Getenv("ASTRLINK_CODEX_OAUTH_CLIENT_ID")); clientID != "" {
		oauth.ClientID = clientID
	}
	if issuer := strings.TrimSpace(os.Getenv("ASTRLINK_CODEX_OAUTH_ISSUER")); issuer != "" {
		oauth.Issuer = issuer
	}
	if apiBase := strings.TrimSpace(os.Getenv("ASTRLINK_CODEX_API_BASE_URL")); apiBase != "" {
		oauth.APIBaseURL = apiBase
	}
	grok := accountauth.OAuthConfig{Provider: contract.SubscriptionProviderXAIGrok}
	if issuer := strings.TrimSpace(os.Getenv("ASTRLINK_GROK_OAUTH_ISSUER")); issuer != "" {
		grok.Issuer = issuer
	}
	if apiBase := strings.TrimSpace(os.Getenv("ASTRLINK_GROK_API_BASE_URL")); apiBase != "" {
		grok.APIBaseURL = apiBase
	}
	// OAuth tokens live in the database, sealed under the local key (plan
	// §5.9). Accounts connected by an earlier release move out of the OS
	// keystore here, before any request can read them.
	accounts := subscription.StorageAccountStore{Store: store}
	credentials := subscription.NewStorageAccountCredentialStore(store)
	if moved := credentials.MigrateKeyringCredentials(ctx, accounts, accountauth.NewKeyringCredentialStore(), logf); moved > 0 {
		logf("astrlink subscription: moved %d account credential(s) from the OS keystore into local storage", moved)
	}
	return subscription.NewManager(accounts, credentials, oauth, grok)
}

// subscriptionRiskReporter lets the inference plane pause subscription
// accounts through the manager that owns their persisted state.
type subscriptionRiskReporter struct{ manager *subscription.Manager }

func (reporter subscriptionRiskReporter) ReportSubscriptionRisk(
	ctx context.Context, id contract.ServiceID, observation contract.SubscriptionRiskObservation,
) error {
	return reporter.manager.ReportRisk(ctx, id, observation)
}

func (reporter subscriptionRiskReporter) ClearExpiredSubscriptionRisk(ctx context.Context, id contract.ServiceID) error {
	return reporter.manager.ClearExpiredRisk(ctx, id)
}

func (reporter subscriptionRiskReporter) RefreshRejectedSubscriptionToken(
	ctx context.Context, id contract.ServiceID, rejectedAccessToken string,
) error {
	return reporter.manager.HandleUnauthorized(ctx, id, rejectedAccessToken)
}
