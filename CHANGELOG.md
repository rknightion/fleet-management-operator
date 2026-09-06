# Changelog

All notable changes to the Fleet Management Operator will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [1.0.2](https://github.com/rknightion/fleet-management-operator/compare/v1.0.1...v1.0.2) (2026-09-06)


### Bug Fixes

* author is Rob Knight, not Rob Knighton ([006d885](https://github.com/rknightion/fleet-management-operator/commit/006d88589a9304c5fa2b53f32907158a9ee5ef21))
* **deps:** update kubernetes libraries to v0.36.4 ([#64](https://github.com/rknightion/fleet-management-operator/issues/64)) ([5bd4ba0](https://github.com/rknightion/fleet-management-operator/commit/5bd4ba0f428576854976cdc4109836fa1959f904))
* **deps:** update module github.com/go-sql-driver/mysql to v1.10.1 ([#85](https://github.com/rknightion/fleet-management-operator/issues/85)) ([5c7fde6](https://github.com/rknightion/fleet-management-operator/commit/5c7fde6f43686a658dd12e559886612ceddc3c85))
* **deps:** update module github.com/onsi/ginkgo/v2 to v2.32.1 ([#58](https://github.com/rknightion/fleet-management-operator/issues/58)) ([fcc6df8](https://github.com/rknightion/fleet-management-operator/commit/fcc6df895fc405acac4efd6640d27fbb69af221d))
* **deps:** update module github.com/onsi/gomega to v1.43.0 ([#69](https://github.com/rknightion/fleet-management-operator/issues/69)) ([35c85ec](https://github.com/rknightion/fleet-management-operator/commit/35c85ec95f2de3306da00759402a6c056a7f2a97))
* **deps:** update module github.com/prometheus/client_model to v0.6.3 ([#83](https://github.com/rknightion/fleet-management-operator/issues/83)) ([f12bd27](https://github.com/rknightion/fleet-management-operator/commit/f12bd27d9ea6ea98c7d74c761412848f7b676b4b))
* **deps:** update module github.com/stretchr/testify to v1.12.0 ([#61](https://github.com/rknightion/fleet-management-operator/issues/61)) ([31d262b](https://github.com/rknightion/fleet-management-operator/commit/31d262bd4bed00fff952af5f474b6b1c8a594f1e))
* **deps:** update module github.com/stretchr/testify to v1.12.1 ([#62](https://github.com/rknightion/fleet-management-operator/issues/62)) ([ef6a34a](https://github.com/rknightion/fleet-management-operator/commit/ef6a34aae8f686154b143ff6346664b3430543ba))
* **deps:** update module k8s.io/apiextensions-apiserver to v0.36.4 ([#66](https://github.com/rknightion/fleet-management-operator/issues/66)) ([5d2665b](https://github.com/rknightion/fleet-management-operator/commit/5d2665bda374868a342ea5e4b3f69d2cac486e6b))
* **deps:** update module k8s.io/client-go to v0.36.4 ([#65](https://github.com/rknightion/fleet-management-operator/issues/65)) ([abeff64](https://github.com/rknightion/fleet-management-operator/commit/abeff6470593a6336da238c9d9f06db1c282aec6))
* **deps:** update opentelemetry to v1.46.0 ([#67](https://github.com/rknightion/fleet-management-operator/issues/67)) ([ff3c300](https://github.com/rknightion/fleet-management-operator/commit/ff3c300d198ced9f0b40d7e376dbcf6f79086d01))
* **docs:** ignore webhook validators so the API reference has no dead anchor ([4a94ace](https://github.com/rknightion/fleet-management-operator/commit/4a94ace33061fe26345d0b4ec8b03909574bdedf))
* **e2e:** stop Run discarding the caller's environment ([49efede](https://github.com/rknightion/fleet-management-operator/commit/49efede23f894d5969de8a2f061f7858fd5c1356))
* **helm:** chomp the permutations block scalar ([540f69b](https://github.com/rknightion/fleet-management-operator/commit/540f69b500df70f5b8096f82619f6f8b27243ddc))
* **hooks:** close three bypasses in the backlog guard ([7b6a83d](https://github.com/rknightion/fleet-management-operator/commit/7b6a83d1e963c2e45259f9e0bb0796a0c4cf3ac3))

## [1.0.1](https://github.com/rknightion/fleet-management-operator/compare/v1.0.0...v1.0.1) (2026-08-10)


### Bug Fixes

* **chart:** stop wearing Grafana's logo, and point home at the docs ([fa537a1](https://github.com/rknightion/fleet-management-operator/commit/fa537a1c3f9ece4ac989e4d0ce85c72a9e96b0c5))
* **ci:** restore package-name so the pending release PR is still recognised ([7f084c3](https://github.com/rknightion/fleet-management-operator/commit/7f084c36ad50dabf86c6c6640cd570fb18408403))
* **ci:** tag releases v1.0.0, not fleet-management-operator-v1.0.0 ([b80f64d](https://github.com/rknightion/fleet-management-operator/commit/b80f64d8a51a36bc0114b5fd1c9dc8bae7f0bde7))
* **deps:** refresh all Go dependencies in one pass ([#56](https://github.com/rknightion/fleet-management-operator/issues/56)) ([9cb8b52](https://github.com/rknightion/fleet-management-operator/commit/9cb8b52859a4461f7959f8c05a0fb74bafe1b820))
* **renovate:** add gomodTidy to postUpdateOptions ([0d5dee4](https://github.com/rknightion/fleet-management-operator/commit/0d5dee4f5bb5362ba2fff12182c9b7283fe94dac))

## 1.0.0 (2026-08-10)


### Features

* **01-01:** enhance FleetAPIError with PipelineID, IsTransient, and Unwrap ([d0311d6](https://github.com/rknightion/fleet-management-operator/commit/d0311d692cc74aa34d6f4c2cfc547583eefdf158))
* **02-01:** add error classification helpers for controller ([810d123](https://github.com/rknightion/fleet-management-operator/commit/810d1232a5dc157842075b8cafe50e052e051e9d))
* **03-01:** add formatConditionMessage, loggerFor helpers and condition transition logging ([70ffa77](https://github.com/rknightion/fleet-management-operator/commit/70ffa770c19618e3869f0bff1ce587311904884d))
* **04-01:** add K8s manifests for mock API and E2E test fixtures ([2d970c0](https://github.com/rknightion/fleet-management-operator/commit/2d970c0ade0bab36518adfd03bb8e88e9bfba49d))
* **04-01:** create mock Fleet Management API server ([bbd89f1](https://github.com/rknightion/fleet-management-operator/commit/bbd89f182351add51010307bdd05c343ab390050))
* **04-02:** deploy mock API in E2E suite before controller ([36f6326](https://github.com/rknightion/fleet-management-operator/commit/36f63267a79bb97b6e1924e394c93f9d47c9165f))
* **04-03:** add GitHub Actions E2E workflow ([5b6c182](https://github.com/rknightion/fleet-management-operator/commit/5b6c182e2756ce69eb1ddb9a3401c8c2eb611e26))
* add Collector / RemoteAttributePolicy / ExternalAttributeSync management ([3cf0961](https://github.com/rknightion/fleet-management-operator/commit/3cf0961b2437c07183113fbd9b5de074804b4f60))
* add CollectorDiscovery auto-mirror controller ([e312be7](https://github.com/rknightion/fleet-management-operator/commit/e312be77c34da89c2d4e4d6a01794310c5d9d877))
* add PipelineDiscovery CRD to import existing Fleet pipelines as Pipeline CRs ([a0b329a](https://github.com/rknightion/fleet-management-operator/commit/a0b329a65c6b51da261e7d7611d09f5ad82d2c67))
* add TenantPolicy CRD with opt-in K8s RBAC tenancy enforcement ([963d836](https://github.com/rknightion/fleet-management-operator/commit/963d83636127bd65d688267d6aa4054e9728f53a))
* **api,tenant:** Batch C — tenant policy correctness, plus D9 webhook markers ([669ef67](https://github.com/rknightion/fleet-management-operator/commit/669ef67665bb6408834162b0a313d95dcd142933))
* **api:** add CEL structural validation rules and matcher cap documentation ([bd2c33a](https://github.com/rknightion/fleet-management-operator/commit/bd2c33a6f332a13627308aaf193d1d528c9d3a3c))
* **api:** add CollectorStatus observed fields ([76ea46c](https://github.com/rknightion/fleet-management-operator/commit/76ea46cb3cd58eb69f6eae1f77b9f37d646df8ca))
* **api:** add SourceTypeGrafana enum value ([c09090a](https://github.com/rknightion/fleet-management-operator/commit/c09090a0404e068248944d158ac424f2a275ab50))
* **api:** add TenantPolicyStatus subresource and status reconciler ([544cceb](https://github.com/rknightion/fleet-management-operator/commit/544cceb1cc809ba4c8f03807fa7bc46bff409fbd))
* **api:** declare Collector.spec.id immutability via CEL schema rule ([7ec6fa3](https://github.com/rknightion/fleet-management-operator/commit/7ec6fa353a7b2a069ed6c654ad1c967f6b1e0c81))
* **api:** mark namespaced CRDs as scope=Namespaced ([5e84845](https://github.com/rknightion/fleet-management-operator/commit/5e84845d1058744e885f672dda3f443ebc59811b))
* **chart:** add webhook Service+VWC, PrometheusRule, Grafana dashboard templates (WH-01,02,DOC-01,02) ([f0ab707](https://github.com/rknightion/fleet-management-operator/commit/f0ab707d3048957a5a0dbf418e21cd09b028223b))
* **controller:** add read-only reconcile path for pipelines ([49f2b87](https://github.com/rknightion/fleet-management-operator/commit/49f2b87e14cabe30f4a84ce106bef976a594604c))
* **controller:** mirror collector Fleet fields into status ([4f4f4b7](https://github.com/rknightion/fleet-management-operator/commit/4f4f4b7ec1d8a8fadbb91741d8643c294a644c95))
* **controllers:** add per-controller MaxConcurrentReconciles (PERF-04, PERF-03) ([d9ba8ef](https://github.com/rknightion/fleet-management-operator/commit/d9ba8efb6c8a2e0db109616769ba59d32c278e89))
* **controllers:** per-target rate limiter on ExternalAttributeSync (E19) ([6bd5351](https://github.com/rknightion/fleet-management-operator/commit/6bd5351c8fbf61970c99d78fe528b7e624f0a473))
* **crd,ci:** add Grafana source type, collector observed fields, and read-only pipeline support ([e8d5ab8](https://github.com/rknightion/fleet-management-operator/commit/e8d5ab8fccee4587c14d0cbf1765b1c3b3afda46))
* **discovery:** set read-only annotation instead of spec.paused ([e1b7181](https://github.com/rknightion/fleet-management-operator/commit/e1b7181e1155b1f4453f498e040030d09de2808c))
* **docgen:** add hack/docgen tool for auto-generating docs ([74baa2a](https://github.com/rknightion/fleet-management-operator/commit/74baa2a3149ae0297ea64c5d18ff5cc48ae1bb24))
* **fleetclient:** add GetPipeline and Grafana source mapping ([637a1ed](https://github.com/rknightion/fleet-management-operator/commit/637a1ed67349882dab25e823a46bd1510533a800))
* **fleetclient:** make rate-limiter rate and burst configurable ([2e8aaf5](https://github.com/rknightion/fleet-management-operator/commit/2e8aaf5b2cd341de891372af3b79e71d3f063980))
* **helm:** expose fleet-api-rps and fleet-api-burst as Helm values ([7124397](https://github.com/rknightion/fleet-management-operator/commit/7124397e6e714b57698d7f56053bdf530aa6e136))
* mint release-please token from the OpenBao broker ([11a4328](https://github.com/rknightion/fleet-management-operator/commit/11a432897f5fa9a909365b1a708f0f4cb6069e5d))
* **mockapi:** implement GetPipeline on mock server ([9a47c06](https://github.com/rknightion/fleet-management-operator/commit/9a47c06d2c9ed69242692589cf77a5bb7e36f7eb))
* **mockapi:** rewrite using connect-go proto handlers ([a4945a0](https://github.com/rknightion/fleet-management-operator/commit/a4945a0879243432a4f7573efce04221cf47b008))
* **obs:** add Fleet API request metrics and rate-limiter wait histogram (OBS-01, OBS-02) ([66c177b](https://github.com/rknightion/fleet-management-operator/commit/66c177b28f1e12f9a6765558f9a79c12c2d1a54f))
* **obs:** merge sync-age histogram, owned-key and discovery-list gauges (OBS-03, OBS-04, OBS-05) ([4212caa](https://github.com/rknightion/fleet-management-operator/commit/4212caa5c2fcad67618643d48c0f83300794828c))
* **obs:** OpenTelemetry tracing for Fleet API calls, noop by default (OBS-07) ([8cab61e](https://github.com/rknightion/fleet-management-operator/commit/8cab61e97ab9df1713fd5d7b43ce65ce1f0c534d))
* **obs:** OpenTelemetry tracing for Fleet API calls, noop by default (OBS-07) ([efeb46e](https://github.com/rknightion/fleet-management-operator/commit/efeb46ed1947f9135461f0f9991d4174e23e7c3c))
* **obs:** reconcile-outcome counters; fix event emission gaps (OBS-06, OBS-08) ([31cb4a2](https://github.com/rknightion/fleet-management-operator/commit/31cb4a24fea02398aea1d6fe678dcf16fd3511a5))
* **obs:** sync-age histogram; owned-key and discovery-list-size gauges (OBS-03, OBS-04, OBS-05) ([e33b588](https://github.com/rknightion/fleet-management-operator/commit/e33b588740d9d42125b9d10d8fdc57bf6f196e5e))
* **pipeline:** opt-in namespace-scoped Fleet naming with safe auto-migration ([#8](https://github.com/rknightion/fleet-management-operator/issues/8)) ([#14](https://github.com/rknightion/fleet-management-operator/issues/14)) ([7439ae4](https://github.com/rknightion/fleet-management-operator/commit/7439ae4dc46ecf406286af12faba55736dadc78a))
* **rbac,docs:** opt-in user roles + security trust-model doc (WS1) ([bf9bd89](https://github.com/rknightion/fleet-management-operator/commit/bf9bd8942e1bb412edfacc3ee0b0425343440fbe))
* **samples:** add description comments to sample CRs ([eb17a27](https://github.com/rknightion/fleet-management-operator/commit/eb17a2743f1fb724fc73f72a901dfebdf4a28924))
* **security:** label-scopeable Secret cache + optional cluster-wide secret drop (WS3) ([13a92b3](https://github.com/rknightion/fleet-management-operator/commit/13a92b3b7a09675b1cb012d2adfcfc40ac5d4603))
* **security:** SSRF dial-time hardening for EAS HTTP source (WS2) ([82a993d](https://github.com/rknightion/fleet-management-operator/commit/82a993da5b8175d5bb0a25250f17ddaa225836cd))
* **security:** SubjectAccessReview + TenantPolicy coverage for discovery CRDs (WS4) ([fcbd631](https://github.com/rknightion/fleet-management-operator/commit/fcbd6314796edbde385459dd77c7c55565360015))
* **webhook:** validate Pipeline spec.name ([#8](https://github.com/rknightion/fleet-management-operator/issues/8), Phase 1) ([#11](https://github.com/rknightion/fleet-management-operator/issues/11)) ([6b89fda](https://github.com/rknightion/fleet-management-operator/commit/6b89fda7361adef7f1001c88de17deef775a812c))
* **webhook:** validate source type and Grafana read-only rule ([080730b](https://github.com/rknightion/fleet-management-operator/commit/080730b277a201d01acff1f24df01b9d85b00d4e))
* wire TenantPolicy enforcement into manager, webhooks, and Helm ([5ba4566](https://github.com/rknightion/fleet-management-operator/commit/5ba45662ab3287c7d7b9f2b89c9cfe55268c1fcd))


### Bug Fixes

* **01-01:** handle io.ReadAll errors in client HTTP response handling ([a25776d](https://github.com/rknightion/fleet-management-operator/commit/a25776d2a45fdcb92bdb8b57e43a2a7b1a951ee2))
* **02-01:** preserve original error in updateStatusError and prevent 404 recursion ([71fbafd](https://github.com/rknightion/fleet-management-operator/commit/71fbafd264a5046465f8ea28065d849d2af06133))
* **02:** revise plan 02-01 based on checker feedback ([20b98c5](https://github.com/rknightion/fleet-management-operator/commit/20b98c5d9af64c6c19a30b2a1385d805ecfb3139))
* add safe event emission helpers to prevent nil pointer dereference in tests ([0968f5a](https://github.com/rknightion/fleet-management-operator/commit/0968f5aeda58433774b41aee071937c07c1d5511))
* **api:** add RemoteAttributePolicy.status.matchedCount and fix printer column ([01237cf](https://github.com/rknightion/fleet-management-operator/commit/01237cf5fc4849da555481efb0fa0f8e7370afe5))
* **api:** harden custom resource validation ([03659c5](https://github.com/rknightion/fleet-management-operator/commit/03659c57daf57cd14156077309c2bda0c72f458e))
* **chart,docs:** Chart.yaml metadata, CHANGELOG template, install troubleshooting (HELM-10, DOC-06,07, SEC-03) ([71c61f6](https://github.com/rknightion/fleet-management-operator/commit/71c61f6277afc5a27975616abcf2410b24b84823))
* **chart:** Batch A — install-blocking helm-chart defects ([4f50569](https://github.com/rknightion/fleet-management-operator/commit/4f50569e8b779f39a51941d1cd2d2defd754fd63))
* **chart:** close edge-case install bugs surfaced by the parallel audit ([51ef7f7](https://github.com/rknightion/fleet-management-operator/commit/51ef7f70ebe6bcda53a997b0250d6949d7acd062))
* **chart:** consolidate duplicate webhook sections in values.yaml ([55f6e28](https://github.com/rknightion/fleet-management-operator/commit/55f6e28e53f7854b52dcc8cade74345c30045ea9))
* **ci:** give release-please a packages block so it can cut a release ([c7faf2c](https://github.com/rknightion/fleet-management-operator/commit/c7faf2c44a75ec80104c9300a4c7e35070c06b3c))
* **ci:** stop the release PR failing its own chart-docs check ([e39ab38](https://github.com/rknightion/fleet-management-operator/commit/e39ab38d140a0eeab9261115341ea9a0198ad27c))
* **ci:** unblock codegen deepcopy generation + clear pre-existing lint ([#13](https://github.com/rknightion/fleet-management-operator/issues/13)) ([a4ba44a](https://github.com/rknightion/fleet-management-operator/commit/a4ba44a8f13c890a4703f940cca379babafcfe25))
* **cmd:** lowercase Fleet URL error messages ([518fcb0](https://github.com/rknightion/fleet-management-operator/commit/518fcb098bc8a677f2e5d63d33e49e7ad62cddfe))
* **controller:** harden Pipeline deletion against read-only and forged-ID deletes ([#4](https://github.com/rknightion/fleet-management-operator/issues/4)) ([b610cfe](https://github.com/rknightion/fleet-management-operator/commit/b610cfeb001cd1ae5f0dd4eea58e4a56cb993815))
* **controller:** improve reconcile safety ([c65b053](https://github.com/rknightion/fleet-management-operator/commit/c65b05365a3fb324ebc13953b0af6fd42e35d79a))
* **controllers:** Batch B — silent correctness regressions in PERF-03 and no-op short-circuits ([82dae03](https://github.com/rknightion/fleet-management-operator/commit/82dae032550035b876bc2b8f17e9fa850b383f17))
* **deploy:** harden operator packaging ([1950191](https://github.com/rknightion/fleet-management-operator/commit/19501912ce17c6842036a0c506b40a57421d2c5e))
* **docs:** make the generators emit what the docs site needs ([2b54038](https://github.com/rknightion/fleet-management-operator/commit/2b540385e5c4d9d42ff3c3563d89b773f1252bcc))
* **docs:** shorten the invalid-samples link past the 120-char lll limit ([f5d1374](https://github.com/rknightion/fleet-management-operator/commit/f5d13748dda6275095ad91a1abd6709a7138a45e))
* **fleetclient:** OTel semconv keys; rate-limit wait observed on cancel ([55c2252](https://github.com/rknightion/fleet-management-operator/commit/55c22529858a5297891ea66eecf216cc0e7c056c))
* **helm:** memory limits, metrics security, logging, security defaults, chart polish (HELM-01..13, SEC-02..04) ([c6ddaa1](https://github.com/rknightion/fleet-management-operator/commit/c6ddaa14a17c8a72d462ba4a842e087394f1b3f6))
* **helm:** wire all new deployment flags and declare container ports (HELM-02,03,04,06,09) ([238d29d](https://github.com/rknightion/fleet-management-operator/commit/238d29d9f07a521db2aaccd0e830e43db3f3d303))
* **helm:** wire leader-election lease flags; production log defaults (HELM-02, HELM-06, UPG-02) ([e9ee9b8](https://github.com/rknightion/fleet-management-operator/commit/e9ee9b818c058620eb6315f0ad38e58a7caf9052))
* **main:** TenantPolicy webhook always-on; OTEL resource error logged; drop unused metrics cert flags ([380ddc4](https://github.com/rknightion/fleet-management-operator/commit/380ddc4cced3b94287569101cb47cb0dd76dc052))
* **obs,main:** Batch D — OTEL footguns, fleet client interceptors, manager lifecycle ([cc5f10a](https://github.com/rknightion/fleet-management-operator/commit/cc5f10a1050f40d331c4d875cc546d28a5dc92d6))
* **sec:** pin Dockerfile base to digest; add image.digest Helm value (SEC-01) ([885570b](https://github.com/rknightion/fleet-management-operator/commit/885570bc868d46063056f1425c7676688482a9df))
* **sec:** pin Dockerfile base to digest; add image.digest Helm value (SEC-01) ([716c15c](https://github.com/rknightion/fleet-management-operator/commit/716c15c214e7a3480ba3be1460a70cf73861261d))
* **security:** register RemoteAttributePolicy webhook when collector controller is enabled ([#7](https://github.com/rknightion/fleet-management-operator/issues/7)) ([6fef595](https://github.com/rknightion/fleet-management-operator/commit/6fef595dfb44270f306f71272173f5a1004d0805))
* **sources:** SQL connection leak; sanitize URL credentials in error messages ([8727e6d](https://github.com/rknightion/fleet-management-operator/commit/8727e6dda7eb212b39b7665f30931d36df7b79d5))
* **test:** enable race detector in test target (TEST-01) ([e7a1e4a](https://github.com/rknightion/fleet-management-operator/commit/e7a1e4a6b0d85053b2265510d787aaa66ee8342c))
* **upg:** Hub conversion markers; change-class policy table in versioning doc (UPG-01, UPG-06) ([614eb96](https://github.com/rknightion/fleet-management-operator/commit/614eb962162900cc2580e65726384ac54fe4b054))
* **upg:** Hub conversion markers; change-class policy table in versioning doc (UPG-01, UPG-06) ([2fca785](https://github.com/rknightion/fleet-management-operator/commit/2fca7850697c6f6179417abe8392b2483321949e))
* webhook port as Helm value + startup cert validation; HTTP conn pool close (WH-04, UPG-03) ([4b54bf0](https://github.com/rknightion/fleet-management-operator/commit/4b54bf04ef6ec27300a95780ace78f94cc3110bb))
* webhook port as Helm value + startup cert validation; HTTP conn pool close (WH-04, UPG-03) ([542a48c](https://github.com/rknightion/fleet-management-operator/commit/542a48c3ba315c5f28dd85d1098411eb0963872f))
* **webhook:** set timeoutSeconds: 5 on all webhook entries (WH-02) ([51de5f1](https://github.com/rknightion/fleet-management-operator/commit/51de5f189a51ceb6d919899c734b85c88906d318))
* **webhook:** validate the incoming object, not the empty receiver (Collector, CollectorDiscovery) ([4edeca3](https://github.com/rknightion/fleet-management-operator/commit/4edeca3a8e1f0f451865dfac395466d1846bfe84))


### Performance Improvements

* selective Collector watch handler via matcher-key IndexField (PERF-03) ([a56ae7e](https://github.com/rknightion/fleet-management-operator/commit/a56ae7e64c7ea9cbed14e50bf289ef8e178f5114))
* selective Collector watch handler via matcher-key IndexField (PERF-03) ([065e546](https://github.com/rknightion/fleet-management-operator/commit/065e546b6302a829c21b8a2fcddec32c237ce48c))
* **status:** cap CollectorDiscovery.status.conflicts at 100 (PERF-06) ([75a3dbe](https://github.com/rknightion/fleet-management-operator/commit/75a3dbeaa2e7bc842e47cba33fbddf88be09db88))
* **status:** cap ExternalAttributeSync.ownedKeys at 1000, add no-op short-circuit (PERF-01) ([c801c66](https://github.com/rknightion/fleet-management-operator/commit/c801c6643a88bb7102efa0940f6f32e0aa90879f))
* **status:** cap RemoteAttributePolicy.matchedCollectorIDs at 1000 (PERF-01) ([62cf4a3](https://github.com/rknightion/fleet-management-operator/commit/62cf4a3bfa093070ffba32b8bbccbfb77cba7ec3))

## [Unreleased]

### Added
- TenantPolicy CRD with opt-in Kubernetes RBAC tenancy enforcement, plus its
  status reconciler (`Ready` / `Valid` conditions, `boundSubjectCount`).
- Collector, RemoteAttributePolicy, ExternalAttributeSync, and CollectorDiscovery
  CRDs, controllers, and admission webhooks (all default-off; opt in per controller).
- External source plugins for ExternalAttributeSync: HTTP (bearer / basic auth)
  and SQL (postgres via `lib/pq`, mysql via `go-sql-driver/mysql`). Both kinds
  ship in this release; the factory in `cmd/main.go` dispatches on
  `spec.source.kind`.
- CEL-based structural validation on CRD schemas: `Collector.spec.id`
  immutability, matcher caps, configType-vs-contents constraints.
- API versioning and graduation policy doc, plus a cross-CRD condition
  type/reason registry.
- Helm chart templates: webhook Service, ValidatingWebhookConfiguration,
  cert-manager Certificate, PodDisruptionBudget, ServiceMonitor, PrometheusRule
  with operator alerts, and an embedded Grafana dashboard ConfigMap (DOC-01/02,
  WH-01/02).
- Operator metrics: Fleet API request counters and rate-limiter wait histogram
  (OBS-01/02); reconcile-outcome counters (OBS-06); sync-age histogram, owned-key
  gauge, discovery-list-size gauge (OBS-03/04/05); OpenTelemetry tracing for
  Fleet API calls, noop by default (OBS-07).
- Per-target rate limiter for ExternalAttributeSync sources (E19): two syncs
  pointing at the same upstream (HTTP host or SQL secret) share a token bucket
  via `--controller-sync-target-rate` and `--controller-sync-target-burst`.
  Default off.
- Per-controller `MaxConcurrentReconciles` (policy=4, sync=4, discovery=1) with
  `--controller-{policy,sync,discovery}-max-concurrent` flags and matching Helm
  values (PERF-04). Pipeline and Collector remain at 1 by design.
- Selective Collector watch handler indexed by matcher key (PERF-03): policy
  changes now wake only the matching Collectors instead of every Collector.
- Helm chart values exposing `fleetManagement.apiRatePerSecond` and
  `fleetManagement.apiBurst` (configurable Fleet API rate limit / burst).
- Helm chart values exposing `image.digest`, `webhook.port`, leader-election
  lease tunables, and probe / security tunables (HELM-02/03/04/06/09).
- Production-readiness audit scorecard (`docs/superpowers/audits/`) and full
  troubleshooting guide / per-alert runbooks / webhook setup guide
  (DOC-03/04/05).
- Sample manifests: annotated invalid-CR examples for onboarding.
- Renovate configuration for dependency updates.
- Auto-generated chart README via helm-docs (`make chart-docs`,
  `make chart-docs-check`).

### Changed
- Memory defaults raised to limits=2Gi / requests=512Mi (HELM-01); 128Mi default
  was insufficient at 30k-Collector informer-cache footprint and would OOMKill.
  Sizing matrix in `values.yaml`.
- Liveness probe `initialDelaySeconds` raised so pods are not killed during
  initial cache warm-up at 30k CRs (HELM-08).
- Production logging defaults: structured JSON output, info level
  (`Development: false`).
- Fleet API HTTP client now closes its connection pool on shutdown (UPG-03);
  webhook port is a Helm value with startup cert validation (WH-04).
- Webhook entries set `timeoutSeconds: 5` (WH-02).
- `RemoteAttributePolicy.status.matchedCollectorIDs` capped at 1000 with
  `matchedCount` field (PERF-01); `ExternalAttributeSync.status.ownedKeys`
  capped at 1000 with no-op short-circuit; `CollectorDiscovery.status.conflicts`
  capped at 100 (PERF-06).
- CLAUDE.md: documented REC reconciler invariants, per-target sync rate limiter,
  per-controller event reasons, and updated SQL plugin to "currently shipped"
  (was Phase-3-only stub).

### Fixed
- Validating webhooks for Collector and CollectorDiscovery now validate the
  incoming `obj`, not the empty receiver. Previously, the framework's empty
  `*Collector{}` / `*CollectorDiscovery{}` receiver was being validated, so
  every admission request trivially passed (WH-05 follow-up).
- PERF-03 silent correctness regressions and no-op short-circuit gaps fixed
  (Batch B).
- TenantPolicy correctness, including D9 webhook markers (Batch C).
- Install-blocking Helm chart defects (Batch A): consolidated duplicate webhook
  sections, fixed RBAC/template inconsistencies.
- Fleet client interceptor / manager lifecycle / OTEL footguns (Batch D).
- Conflict-policy reconcile path now treats `ctrl.Result.Requeue=true` from
  status-conflict as cache lag (no error, no exponential backoff).
- 404 from Fleet API on Pipeline / Collector deletion is treated as success.
- Helm chart: leader-election lease flags wired; production log defaults
  applied; container ports declared on Deployment; metrics endpoint properly
  bound (HELM-02/04/06).
- Chart README: regenerated from `values.yaml` (helm-docs); previous manual
  table claimed `limits.memory: 128Mi`, drift since HELM-01 fix.
- Documentation: deployment / Secret / webhook-Service names now match the
  actual chart-rendered names; memory-limit references updated to reflect the
  2Gi default.
- Test: graceful-shutdown test now drives a real `PipelineReconciler` to verify
  context propagation through the full reconciler → client → interceptor chain
  (E1, replaces tautological stub).
- Lint: `make lint` from 44 issues to 0 (modernize/prealloc/errcheck/unused
  cleanups; no behaviour change).

### Security
- Container image base pinned to digest; added `image.digest` Helm value (SEC-01).
- Pod and container security context hardened: non-root, read-only root FS,
  dropped capabilities, restricted seccomp profile (SEC-02/03/04).
- Race detector enabled in `make test` target (TEST-01).

### Upgrade Notes
- Helm value renames: `fleet-management-credentials` Secret is now
  `<release>-credentials` (default `fleet-management-operator-credentials`).
  Existing self-managed Secrets continue to work via
  `fleetManagement.existingSecret.name`.
- Webhook Service is now `<release>-webhook` (was `<release>-webhook-service`).
- New CRDs (`Collector`, `RemoteAttributePolicy`, `ExternalAttributeSync`,
  `CollectorDiscovery`, `TenantPolicy`) install with the chart; controllers
  remain disabled until you set `controllers.<name>.enabled: true`.
- `controllers.collectorDiscovery.enabled: true` requires
  `controllers.collector.enabled: true`; the manager refuses to start otherwise.

## [0.1.0] - YYYY-MM-DD

### Added
- Initial release of Fleet Management Operator
- Pipeline CRD for managing Fleet Management pipelines
- Support for Alloy and OpenTelemetry Collector configurations
- Multi-architecture Docker images (linux/amd64, linux/arm64)
- Helm chart for easy deployment
- Source tracking (Git, Terraform, Kubernetes)
- Finalizer support for proper cleanup
- Status conditions following Kubernetes conventions
- Metrics endpoint on port 8080
- Leader election for high availability

[Unreleased]: https://github.com/grafana/fleet-management-operator/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/grafana/fleet-management-operator/releases/tag/v0.1.0
