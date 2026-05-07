# Windows Publisher Rebuild Plan

## Product Definition

This desktop product is a Windows 10 / Windows 11 service publisher for the current machine.

Its core job is to:

- manage local services on the current Windows machine
- bind those local services to cloud-side public entries
- verify that cloud port / domain access reaches the local service
- keep publish status, verification status, and failure guidance visible

This is not a multi-machine operations console.
This is not a node/operator control panel shell.

## Hard Deletions

The following product-layer structures must disappear from the user-facing UI:

- local mode
- operator mode
- machine-first home page
- node control panel as a primary home section
- tunnel type workspace as the main information architecture
- node_console / operator_console semantics in top-level navigation

Control-v1 remains allowed only as an execution-layer dependency.

## Target Stack

- desktop shell: Tauri 2
- frontend: React + TypeScript + Vite
- routing: React Router
- server state: TanStack Query
- client state: Zustand
- forms: react-hook-form
- validation: zod
- primary package target: MSI
- secondary package target: portable zip

## Layering

### UI Layer

Responsibilities:

- pages
- components
- empty states
- failure states
- next-step presentation
- form interactions

Not responsible for:

- direct control-contract composition
- tray / window lifecycle
- transport details

### Desktop Shell Layer

Responsibilities:

- window lifecycle
- tray
- config directory
- log directory
- notifications
- startup / exit / minimize-to-tray
- packaging entrypoints

Not responsible for:

- publish rule evaluation
- local service business logic
- verification logic

### Application Service Layer

Responsibilities:

- local service management
- publish rule management
- cloud entry derivation
- rule state evaluation
- verification aggregation
- diagnostics aggregation
- shared next-step generation

This is the core desktop product layer.

### Execution & Transport Layer

Responsibilities:

- reuse desktop-core API/types
- call server-api
- call probe/update/control action
- encapsulate request and response mapping

Allowed reuse:

- desktop-core API/types
- control-v1 execution path
- tunnel/runtime/health/probe machine fields

## Domain Model

### LocalService

- id
- name
- targetHost
- targetPort
- note
- detectedStatus
- lastCheckedAt

### PublishRule

- id
- localServiceId
- protocol
- transportPolicy
- tunnelId
- status
- runtimeState
- runtimePath
- healthStatus
- lastFailureReason
- createdAt
- updatedAt

### CloudEntry

- entryType
- publicHost
- publicPort
- domain
- publicUrl
- tlsMode
- domainReady

### AccessVerification

- ruleId
- probeSupported
- lastProbeSuccess
- lastProbeStatusCode
- lastProbeError
- lastProbeTargetEntry
- lastProbeAt

### RuleStateEvaluation

- active
- entryUsable
- domainReady
- runtimeUnavailable
- hasFailure
- attention
- partial
- badges
- messages
- nextStep

## Final Navigation

The final product navigation is fixed to six sections:

1. Dashboard
2. Local Services
3. Publish Rules
4. Access Verification
5. Diagnostics & Logs
6. Settings

## Page Tree

```text
App Shell
|- DashboardPage
|- LocalServicesPage
|  |- LocalServiceList
|  |- LocalServiceEditor
|- PublishRulesPage
|  |- PublishRuleComposer
|  |- PublishRuleList
|  |- PublishRuleWorkbench
|- AccessVerificationPage
|  |- VerificationSummary
|  |- VerificationList
|  |- VerificationDetail
|- DiagnosticsPage
|  |- DiagnosticsSummary
|  |- FailureFeed
|  |- LogsActions
|- SettingsPage
   |- AccountCard
   |- ApiConfigCard
   |- DeviceBindingCard
   |- TrayCard
   |- PathsCard
```

## Primary Workflow

```text
Login
-> Bind current device
-> Add local service
-> Select publish protocol
-> Bind cloud port or domain
-> Create publish rule
-> Observe runtime state
-> Verify public access
-> Review failure guidance when needed
-> Pause / resume / edit binding
```

## Key Wireframes

### Dashboard

```text
+--------------------------------------------------------------+
| Device Status | Cloud Status | Published Rules | Attention   |
+--------------------------------------------------------------+
| Protocol Readiness                                         |
| HTTP | HTTPS | TCP | UDP | SOCKS5 | P2P(partial)           |
+--------------------------------------------------------------+
| Recent Verification | Recent Failures | Global Next Step    |
+--------------------------------------------------------------+
```

### Publish Rules

```text
+-------------------+------------------------------------------+
| Rule List         | Rule Workbench                           |
| - Service A       | Local Service                            |
| - Service B       | Protocol                                 |
| - Service C       | Cloud Entry                              |
|                   | Runtime / Health / Failure               |
|                   | Copy / Open / Probe / Pause / Resume     |
|                   | Next Step                                |
+-------------------+------------------------------------------+
```

### Access Verification

```text
+--------------------------------------------------------------+
| Public Entry | Protocol | Last Probe | Result | Actions      |
| domain/url   | HTTP     | 2 min ago  | pass   | copy/open    |
| host:port    | TCP      | unsupported| -      | copy/cmd     |
+--------------------------------------------------------------+
| Verification Detail                                         |
| target entry / status code / error / next step              |
+--------------------------------------------------------------+
```

### Diagnostics & Logs

```text
+--------------------------------------------------------------+
| Failure Category | Message | Rule | Next Step                |
+--------------------------------------------------------------+
| Open Logs Directory | Export Diagnostics | Config Paths       |
+--------------------------------------------------------------+
```

## State Coverage

### Global

- unauthenticated
- device unbound
- cloud api unreachable
- local relay unavailable
- connected
- limited capability

### Local Service

- empty
- configured but unpublished
- published
- local target unreachable

### Publish Rule

- draft
- active
- paused
- pending
- unavailable
- health degraded
- execution failure
- probe failure

### Entry & Protocol

- http usable
- https missing domain
- entry not configured
- entry not directly usable
- probe unsupported
- verification not run
- last verification pass
- last verification fail
- p2p partial
- protocol unavailable on current device
- protocol empty

All overview cards, empty states, workbench states, and verification states must derive from the same RuleStateEvaluation.

## Component Inventory

- AppSidebar
- ShellHeader
- StatusHero
- GlobalNextStepPanel
- LocalServiceList
- LocalServiceForm
- PublishRuleComposer
- PublishRuleList
- PublishRuleWorkbench
- CloudEntryCard
- RuntimeStateCard
- VerificationList
- VerificationResultCard
- DiagnosticsFeed
- LogsDirectoryActions
- SettingsCards

## Route & Module Mapping

- `/` -> dashboard
- `/services` -> local services
- `/publish` -> publish rules
- `/verification` -> access verification
- `/diagnostics` -> diagnostics and logs
- `/settings` -> settings

Module mapping:

- `apps/desktop-console/src/App.tsx`: root shell and route entry
- `apps/desktop-console/src/styles.css`: new Windows desktop visual language
- future `apps/desktop-console/src/app-services/*`: application service layer
- future `apps/desktop-console/src/pages/*`: page layer
- future `apps/desktop-console/src/components/*`: reusable UI layer
- future `apps/desktop-console/src-tauri/*`: desktop shell layer

## Replacement Table

| Old Structure | Action | New Home |
| --- | --- | --- |
| local mode / operator mode | delete | none |
| machine list | remove from primary nav | diagnostics / device summary only |
| node control panel | downgrade | execution layer advanced tools |
| tunnel type workspace | replace | publish rules page |
| control result block as main narrative | downgrade | action feedback inside workbench |
| protocol overview by selected node | replace | dashboard readiness |

## MVP Scope

The first Windows installable MVP must support:

- Windows 10 / 11
- HTTP / HTTPS / TCP / UDP / SOCKS5 main path
- P2P as partial / non-blocking
- local service creation
- publish rule creation
- cloud entry binding
- runtime / health / failure visibility
- copy / open / probe where supported
- pause / resume / edit binding
- diagnostics and next-step guidance

Out of scope for MVP:

- full auto-update
- full P2P data plane
- multi-machine operations console
- advanced ACL
- advanced UDP session governance
- advanced SOCKS5 auth

## Windows Runtime & Packaging Plan

- shell: Tauri 2
- tray resident: required
- close window: minimize to tray by default
- explicit exit: tray menu exit
- config dir: `%APPDATA%/CloudRelayPublisher/`
- log dir: `%LOCALAPPDATA%/CloudRelayPublisher/logs/`
- artifact target: `CloudRelayPublisherSetup-x64.msi`
- portable fallback: `CloudRelayPublisher-x64-portable.zip`

## Implementation Order

1. remove old product structure and ship the new page skeleton
2. connect application service layer and shared RuleStateEvaluation
3. connect execution-layer capabilities
4. add tray, logs, config paths, and MSI packaging

No gradual migration path keeps the old dual-mode shell alive.
