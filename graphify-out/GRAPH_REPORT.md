# Graph Report - .  (2026-05-24)

## Corpus Check
- Corpus is ~38,871 words - fits in a single context window. You may not need a graph.

## Summary
- 667 nodes · 989 edges · 46 communities (35 shown, 11 thin omitted)
- Extraction: 83% EXTRACTED · 17% INFERRED · 0% AMBIGUOUS · INFERRED: 170 edges (avg confidence: 0.8)
- Token cost: 279,855 input · 0 output

## Community Hubs (Navigation)
- [[_COMMUNITY_Agent Command Dispatch|Agent Command Dispatch]]
- [[_COMMUNITY_Config Loading|Config Loading]]
- [[_COMMUNITY_Deploy Diff & Planning|Deploy Diff & Planning]]
- [[_COMMUNITY_Deploy Dispatch Pipeline|Deploy Dispatch Pipeline]]
- [[_COMMUNITY_HTTP Control Plane & Debounce|HTTP Control Plane & Debounce]]
- [[_COMMUNITY_Architecture Concepts|Architecture Concepts]]
- [[_COMMUNITY_Compose Driver Operations|Compose Driver Operations]]
- [[_COMMUNITY_Ledger Store & Registry|Ledger Store & Registry]]
- [[_COMMUNITY_Agent Auth & gRPC Server|Agent Auth & gRPC Server]]
- [[_COMMUNITY_Secret Polling & Check|Secret Polling & Check]]
- [[_COMMUNITY_Deployed Set & mTLS|Deployed Set & mTLS]]
- [[_COMMUNITY_Rolling Update Internals|Rolling Update Internals]]
- [[_COMMUNITY_Caddy Route Building|Caddy Route Building]]
- [[_COMMUNITY_Infisical Webhook Handling|Infisical Webhook Handling]]
- [[_COMMUNITY_Drift Reconciliation|Drift Reconciliation]]
- [[_COMMUNITY_Enroll & HTTP Tests|Enroll & HTTP Tests]]
- [[_COMMUNITY_Volume Policy Parsing|Volume Policy Parsing]]
- [[_COMMUNITY_Compose Driver Setup|Compose Driver Setup]]
- [[_COMMUNITY_Config Types|Config Types]]
- [[_COMMUNITY_Env Render & Check Tests|Env Render & Check Tests]]
- [[_COMMUNITY_Webhook HMAC Verify|Webhook HMAC Verify]]
- [[_COMMUNITY_Service Lifecycle Store|Service Lifecycle Store]]
- [[_COMMUNITY_Secret Path Resolution|Secret Path Resolution]]
- [[_COMMUNITY_Caddy Sidecar Manager|Caddy Sidecar Manager]]
- [[_COMMUNITY_Ledger Tests|Ledger Tests]]
- [[_COMMUNITY_Rolling Check Warnings|Rolling Check Warnings]]
- [[_COMMUNITY_Secret Dependency Mapping|Secret Dependency Mapping]]
- [[_COMMUNITY_Webhook Handler Tests|Webhook Handler Tests]]
- [[_COMMUNITY_Dotenv Loading|Dotenv Loading]]
- [[_COMMUNITY_Check Report|Check Report]]
- [[_COMMUNITY_Build & Lint Tooling|Build & Lint Tooling]]
- [[_COMMUNITY_Test Repo Fixtures|Test Repo Fixtures]]
- [[_COMMUNITY_Claude Settings|Claude Settings]]
- [[_COMMUNITY_Release Pipeline|Release Pipeline]]
- [[_COMMUNITY_Lifecycle Queries|Lifecycle Queries]]
- [[_COMMUNITY_Example IaC Repo|Example IaC Repo]]
- [[_COMMUNITY_Synology Driver|Synology Driver]]
- [[_COMMUNITY_Caddy Network Routing|Caddy Network Routing]]
- [[_COMMUNITY_Driver Interface|Driver Interface]]
- [[_COMMUNITY_Synology Agent Script|Synology Agent Script]]
- [[_COMMUNITY_Service Lifecycle Model|Service Lifecycle Model]]
- [[_COMMUNITY_Webhook Payload|Webhook Payload]]
- [[_COMMUNITY_Claude Permissions|Claude Permissions]]
- [[_COMMUNITY_Token Credentials|Token Credentials]]
- [[_COMMUNITY_HTTP Bearer Auth|HTTP Bearer Auth]]

## God Nodes (most connected - your core abstractions)
1. `GitSyncer` - 25 edges
2. `writeFile()` - 21 edges
3. `Load()` - 18 edges
4. `Open()` - 13 edges
5. `HTTPServer` - 13 edges
6. `newSecretsSyncer()` - 12 edges
7. `loadService()` - 11 edges
8. `newHTTPTestServer()` - 11 edges
9. `ComposeDriver.rollingApply` - 11 edges
10. `Store` - 10 edges

## Surprising Connections (you probably didn't know these)
- `Two-axis secret fetch (environment + folder)` --references--> `ResolveSecretsPaths`  [INFERRED]
  docs/configuration.md → internal/config/secretpath.go
- `Synology boot-up agent script` --references--> `Synology DSM driver`  [EXTRACTED]
  deploy/synology/shuttle-agent.sh → docs/operations.md
- `GitHub Actions deploy webhook workflow` --references--> `Configuration Reference`  [INFERRED]
  examples/deploy-workflow.yml → docs/configuration.md
- `TestDeleteVolumesPolicy_UnmarshalYAML()` --calls--> `strictDecode()`  [INFERRED]
  internal/config/volumes_test.go → internal/config/loader.go
- `TestStatus_relativeWorkDir()` --calls--> `writeFile()`  [INFERRED]
  internal/agent/compose_test.go → internal/config/loader_test.go

## Hyperedges (group relationships)
- **Git-push deploy pipeline** — http_api_webhook, architecture_gitsyncer, architecture_computeplan, readme_agent, readme_sqlite_ledger [EXTRACTED 0.90]
- **mTLS dev stack composition** — deploy_docker_compose, deploy_docker_compose_mtls, deploy_config_mtls_example, architecture_agent_auth [EXTRACTED 0.85]
- **CI quality gates** — workflows_push, buf_buf, golangci_golangci [EXTRACTED 0.85]
- **Zero-downtime rolling update flow** — agent_rolling_rollingapply, agent_rolling_waithealthy, agent_caddy_connectcontainers, agent_rolling_removecontainers [EXTRACTED 1.00]
- **Agent command dispatch handlers** — agent_client_handlecommand, agent_client_executedeploy, agent_client_executerollback, agent_client_executeteardown [EXTRACTED 1.00]
- **delete_volumes policy parsing chain** — config_volumes_deletevolumespolicy, config_volumes_normalize, config_volumes_parsehumanduration [EXTRACTED 1.00]
- **Service lifecycle teardown and volume purge** — ledger_lifecycle_markservicepresent, ledger_lifecycle_markserviceremoved, ledger_lifecycle_servicesawaitingteardown, ledger_lifecycle_servicesawaitingpurge [EXTRACTED 1.00]
- **Secret poll fingerprint-diff redeploy** — orchestrator_secretpoll_tick, orchestrator_secretpoll_diffscopes, orchestrator_secretpoll_fingerprintsecrets, orchestrator_gitsyncer_forcedeploy [EXTRACTED 1.00]
- **gRPC TLS credential builders** — mtls_mtls_servercreds, mtls_mtls_servertlscreds, mtls_mtls_clienttlscreds, mtls_mtls_clientcreds [EXTRACTED 1.00]
- **Deploy dispatch pipeline** — orchestrator_git_reconcile, orchestrator_diff_computeplan, orchestrator_git_dispatch, orchestrator_registry_send [EXTRACTED 1.00]
- **Drift reconciliation loop** — orchestrator_reconcile_driftreconciler, orchestrator_reconcile_tick, orchestrator_reconcile_statetracker, orchestrator_git_forcedeploy [EXTRACTED 1.00]
- **Infisical selective redeploy flow** — orchestrator_http_handleinfisicalwebhook, orchestrator_debounce_changedebouncer, orchestrator_secretdeps_servicesusingsecret, orchestrator_http_reconcilesecretchanges [EXTRACTED 1.00]
- **Webhook parse verification flow** — webhook_handler_parse, webhook_hmac_verifysignature, webhook_handler_computenonce, webhook_handler_noncestore [EXTRACTED 0.95]
- **Caddy config assembly** — orchestrator_caddy_routesforhost, orchestrator_caddy_buildcaddyconfig, orchestrator_caddy_hostcaddyconfigjson, orchestrator_caddy_caddyroute [EXTRACTED 0.85]
- **HMAC MAC computation** — webhook_hmac_verifysignature, webhook_hmac_computeheader, webhook_hmac_computemac [EXTRACTED 0.95]

## Communities (46 total, 11 thin omitted)

### Community 0 - "Agent Command Dispatch"
Cohesion: 0.07
Nodes (39): caddySidecar.apply, caddySidecar.connectContainers, caddySidecar.connectProject, caddySidecar.docker, caddySidecar.ensure, connectToCaddy(), containsAny(), containsError() (+31 more)

### Community 1 - "Config Loading"
Cohesion: 0.08
Nodes (36): argvStubDriver(), drain(), TestDown_invokesComposeDown(), TestDown_missingWorkspaceIsNoop(), fileExists(), Load(), loadHosts(), LoadOrchestratorConfig() (+28 more)

### Community 2 - "Deploy Diff & Planning"
Cohesion: 0.09
Nodes (14): GitSyncer, Action, CurrentState, ComputePlan(), CurrentState, Plan, TestComputePlan_allDeploy(), TestComputePlan_noop() (+6 more)

### Community 3 - "Deploy Dispatch Pipeline"
Cohesion: 0.06
Nodes (39): infisical.Handler, Handler.Parse, Payload, Test pings accepted unsigned, Step, GitSyncer.applyRoutes, GitSyncer.DeployAtSHA, GitSyncer.dispatch (+31 more)

### Community 4 - "HTTP Control Plane & Debounce"
Cohesion: 0.07
Nodes (15): HTTPServer, HTTPServer, changeDebouncer, newChangeDebouncer(), TestChangeDebouncer_coalesces(), TestChangeDebouncer_zeroWindowSync(), hostExists(), EnrollOptions (+7 more)

### Community 5 - "Architecture Concepts"
Cohesion: 0.07
Nodes (35): Agent auth (mTLS or token), Agent dials out (no inbound ports), Append-only ledger state model, Shuttle Architecture, Caddy sidecar on shared network, ComputePlan (diff), DriftReconciler, GitSyncer (+27 more)

### Community 6 - "Compose Driver Operations"
Cohesion: 0.09
Nodes (13): ComposeDriver.Down, streamCommand(), ComposeDriver, Store, deploys table, webhook_nonces table, service_lifecycle table, Store.MarkServicePresent (+5 more)

### Community 7 - "Ledger Store & Registry"
Cohesion: 0.10
Nodes (27): DeployRecord, Status, Open(), runMigrations(), ledger.Store, TriggeredBy, agentConn, NewCaddyClient() (+19 more)

### Community 8 - "Agent Auth & gRPC Server"
Cohesion: 0.11
Nodes (17): AgentServiceServer, bearerFromContext(), Token pinned to host so it can't register another, ctxWithToken(), TestTokenStreamInterceptor(), tokenHostFromContext(), TokenStreamInterceptor(), authStream (+9 more)

### Community 9 - "Secret Polling & Check"
Cohesion: 0.11
Nodes (18): config.ResolveSecretsPaths, Append-only ledger, single writer, GitSyncer.Check, CheckReport / ServiceCheck, GitSyncer.checkService, Collect all problems, no fail-fast, GitSyncer.rollingCheck, rollingWarnings (+10 more)

### Community 10 - "Deployed Set & mTLS"
Cohesion: 0.15
Nodes (17): deployedSet, newDeployedSet(), Run(), deployedSet.seedFromDisk, TestSeedFromDisk(), TestSeedFromDisk_missingBaseDir(), OrchestratorConfig.MTLSEnabled, ClientCreds() (+9 more)

### Community 11 - "Rolling Update Internals"
Cohesion: 0.20
Nodes (9): diffIDs(), emit(), emitErr(), shortID(), targetScale(), TestDiffIDs(), TestShortID(), TestTargetScale() (+1 more)

### Community 12 - "Caddy Route Building"
Cohesion: 0.19
Nodes (16): CaddyClient.ApplyRoutes, buildCaddyConfig(), CaddyClient, CaddyRoute, HostCaddyConfigJSON(), parseSnippet(), RoutesForHost(), RoutesFromRepo() (+8 more)

### Community 13 - "Infisical Webhook Handling"
Cohesion: 0.14
Nodes (15): changeDebouncer, changeDebouncer.flush, SecretChange, changeDebouncer.Trigger, HTTPServer.handleInfisicalWebhook, Handler, computeNonce(), webhook.Handler (+7 more)

### Community 14 - "Drift Reconciliation"
Cohesion: 0.18
Nodes (10): DriftReconciler, isRunning(), NewStateTracker(), TestIsRunning(), testRepo(), TestStateTracker_DriftedServices(), TestStateTracker_MissingReportSkipped(), TestStateTracker_StaleIsDrift() (+2 more)

### Community 15 - "Enroll & HTTP Tests"
Cohesion: 0.26
Nodes (14): newEnrollServer(), TestEnroll_success(), TestEnroll_unauthorized(), TestEnroll_unknownHost(), TestListHosts(), authedRequest(), newHTTPTestServer(), openTestLedger() (+6 more)

### Community 16 - "Volume Policy Parsing"
Cohesion: 0.20
Nodes (10): deleteVolumesPolicy, deleteVolumesPolicy.UnmarshalYAML, delete_volumes defaults to manual, normalizeDeleteVolumes, normalizeDeleteVolumes(), ParseHumanDuration(), splitNumberUnit(), TestDeleteVolumesPolicy_UnmarshalYAML() (+2 more)

### Community 17 - "Compose Driver Setup"
Cohesion: 0.20
Nodes (12): ApplyParams, NewComposeDriver(), NewDriver(), NewSynologyDriver(), stubDriver(), TestComposeArgs(), TestNewDriver(), TestStatus_includesStderr() (+4 more)

### Community 18 - "Config Types"
Cohesion: 0.14
Nodes (9): Host, hostsFile, LocalCompose, OrchestratorConfig, RemotePointer, Service, serviceFile, ServiceSource (+1 more)

### Community 20 - "Env Render & Check Tests"
Cohesion: 0.26
Nodes (10): TestCheckService_allPresent(), TestCheckService_noProviderSkips(), TestCheckService_noSchemaSkips(), TestCheckService_reportsMissingKeys(), newSecretsSyncer(), TestRenderEnv_baseAndServiceMerge(), TestRenderEnv_explicitSecretPathOverridesTemplate(), TestRenderEnv_missingSchemaKeyErrors() (+2 more)

### Community 21 - "Webhook HMAC Verify"
Cohesion: 0.25
Nodes (6): Handler, Payload, ComputeHeader(), computeMAC(), parseSignatureHeader(), VerifySignature()

### Community 23 - "Secret Path Resolution"
Cohesion: 0.20
Nodes (8): isAbsSecretPath, ResolveSecretsPaths, Configuration Reference, Feature gating (git sync, Caddy, TLS, token auth), Infisical universal-auth provider env vars, Strict YAML config parsing, Two-axis secret fetch (environment + folder), GitHub Actions deploy webhook workflow

### Community 24 - "Caddy Sidecar Manager"
Cohesion: 0.33
Nodes (3): newCaddySidecar(), CaddyOptions, caddySidecar

### Community 25 - "Ledger Tests"
Cohesion: 0.33
Nodes (8): TestServiceLifecycle(), TestServiceLifecycle_volumePurge(), openMemory(), TestConcurrentWrites(), TestRecordAndMarkStatus(), TestRollbackTarget(), TestRollbackTarget_notEnoughHistory(), TestSeenNonce()

### Community 26 - "Rolling Check Warnings"
Cohesion: 0.29
Nodes (5): GitSyncer, portPublishesFixedHost(), rollingWarnings(), TestRollingWarnings(), TestRollingWarnings_recreateNotInspected()

### Community 27 - "Secret Dependency Mapping"
Cohesion: 0.28
Nodes (4): GitSyncer, normalizeFolder(), sameFolder(), TestSameFolder()

### Community 28 - "Webhook Handler Tests"
Cohesion: 0.31
Nodes (5): fakeNonces, buildRequest(), TestParse_badSignature(), TestParse_replay(), TestParse_valid()

### Community 29 - "Dotenv Loading"
Cohesion: 0.36
Nodes (6): LoadDotEnv(), parseDotEnvLine(), Real environment always wins, TestLoadDotEnv(), TestLoadDotEnv_malformed(), TestLoadDotEnv_missingFileOK()

### Community 31 - "Build & Lint Tooling"
Cohesion: 0.50
Nodes (4): buf module + lint config, buf code generation config, golangci-lint v2 config, CI push workflow

### Community 32 - "Test Repo Fixtures"
Cohesion: 0.67
Nodes (3): app service fixture, app docker-compose fixture, hosts.yaml fixture

### Community 34 - "Release Pipeline"
Cohesion: 1.00
Nodes (3): GoReleaser config, GoReleaser release pipeline, Release workflow

### Community 35 - "Lifecycle Queries"
Cohesion: 0.67
Nodes (3): Store.queryLifecycle, Store.ServicesAwaitingPurge, Store.ServicesAwaitingTeardown

### Community 36 - "Example IaC Repo"
Cohesion: 0.67
Nodes (3): Example hosts.yaml, whoami docker-compose.yml, whoami service definition

### Community 37 - "Synology Driver"
Cohesion: 1.00
Nodes (3): Synology DSM driver, Synology agent install guide, Synology boot-up agent script

## Ambiguous Edges - Review These
- `Append-only ledger, single writer` → `GitSyncer.checkService`  [AMBIGUOUS]
  internal/orchestrator/check.go · relation: conceptually_related_to

## Knowledge Gaps
- **90 isolated node(s):** `shuttle-agent.sh script`, `NonceStore`, `Payload`, `Status`, `TriggeredBy` (+85 more)
  These have ≤1 connection - possible missing edges or undocumented components.
- **11 thin communities (<3 nodes) omitted from report** — run `graphify query` to explore isolated nodes.

## Suggested Questions
_Questions this graph is uniquely positioned to answer:_

- **What is the exact relationship between `Append-only ledger, single writer` and `GitSyncer.checkService`?**
  _Edge tagged AMBIGUOUS (relation: conceptually_related_to) - confidence is low._
- **Why does `writeFile()` connect `Config Loading` to `Compose Driver Operations`, `Ledger Store & Registry`, `Deployed Set & mTLS`, `Compose Driver Setup`, `Dotenv Loading`?**
  _High betweenness centrality (0.150) - this node is a cross-community bridge._
- **Why does `Load()` connect `Config Loading` to `Secret Polling & Check`, `Deploy Diff & Planning`, `Drift Reconciliation`?**
  _High betweenness centrality (0.134) - this node is a cross-community bridge._
- **Why does `Open()` connect `Ledger Store & Registry` to `Ledger Tests`, `Dotenv Loading`, `Compose Driver Operations`, `Enroll & HTTP Tests`?**
  _High betweenness centrality (0.123) - this node is a cross-community bridge._
- **Are the 16 inferred relationships involving `writeFile()` (e.g. with `issueCert()` and `TestClientCreds_badCA()`) actually correct?**
  _`writeFile()` has 16 INFERRED edges - model-reasoned connections that need verification._
- **Are the 10 inferred relationships involving `Load()` (e.g. with `TestLoadService_relativeSecretPathRejected()` and `TestLoad_fixture()`) actually correct?**
  _`Load()` has 10 INFERRED edges - model-reasoned connections that need verification._
- **Are the 9 inferred relationships involving `Open()` (e.g. with `openMemory()` and `LoadDotEnv()`) actually correct?**
  _`Open()` has 9 INFERRED edges - model-reasoned connections that need verification._