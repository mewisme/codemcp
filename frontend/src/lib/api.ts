import { browserOperationHeaders } from "@/lib/operations"

export type Tool = {
  name: string
  title?: string
  description?: string
  inputSchema?: unknown
  outputSchema?: unknown
  annotations?: Record<string, unknown>
}
export type Workspace = {
  id: string
  path: string
  allow_dirs?: string[]
  available?: boolean
  error?: string
}
export type WorkspaceContainer = {
  id: string
  name: string
  workspace_ids?: string[]
}
export type ActivityEvent = {
  sequence?: number
  call_id?: string
  kind: string
  phase?: string
  method?: string
  source?: string
  tool?: string
  workspace_id?: string
  status?: string
  duration_ms?: number
  message?: string
  timestamp: string
}
export type DiagnosticMeta = {
  redacted?: boolean
  truncated?: boolean
}
export type ToolCallDetail = ActivityEvent & {
  request?: unknown
  response?: unknown
  error?: unknown
  diagnostic?: DiagnosticMeta
}
export type ExecutionStatus =
  "running" | "success" | "failed" | "cancelled" | "timed_out" | string
export type ExecutionInfo = {
  id: string
  workspace_id: string
  tool: string
  command: string
  requested_command?: string
  effective_command?: string
  cwd: string
  source?: string
  started_at: string
  finished_at?: string
  status: ExecutionStatus
  exit_code?: number
  timed_out?: boolean
}
export type ExecutionSnapshot = {
  execution: ExecutionInfo
  stdout: string
  stderr: string
  latest_sequence: number
}
export type ExecutionEvent = {
  sequence: number
  type: "output" | "completed" | string
  execution_id: string
  stream?: "stdout" | "stderr" | string
  data?: string
  status?: ExecutionStatus
  exit_code?: number
  timed_out?: boolean
  timestamp: string
}
export type ExecutionFeedEvent = {
  sequence: number
  type: "started" | "output" | "completed" | string
  execution_id: string
  workspace_id: string
  execution?: ExecutionInfo
  stream?: "stdout" | "stderr" | string
  data?: string
  status?: ExecutionStatus
  exit_code?: number
  timed_out?: boolean
  timestamp: string
}
export type ExecutionFeedSnapshot = {
  events: ExecutionFeedEvent[]
  executions: ExecutionInfo[]
  latest_sequence: number
}
export type InstructionSourcePolicy = {
  enabled?: boolean
  context?: boolean
  rules?: boolean
  skills?: boolean
}
export type GlobalInstructionRule = {
  id: string
  name?: string
  enabled: boolean
  content: string
}
export type InstructionSource = {
  provider: string
  kind: "context" | "rules" | "skills" | string
  paths: string[]
  count: number
  enabled: boolean
  loaded: boolean
}
export type GlobalInstructions = {
  version: number
  context: string
  rules: GlobalInstructionRule[]
  source_policy: Record<string, InstructionSourcePolicy>
  detected_sources: InstructionSource[]
}
export type PromptDefinition = {
  version: number
  name: string
  description?: string
  arguments?: { name: string; description?: string; required?: boolean }[]
  messages: {
    role: "user" | "assistant"
    content: { type: "text"; text: string }
  }[]
}
export type ScopedPrompt = {
  scope: "global" | "workspace"
  definition: PromptDefinition
}
export type InstructionRule = {
  path: string
  source: string
  patterns?: string[]
  content: string
  always_apply?: boolean
}
export type InstructionSkill = {
  name: string
  description?: string
  path?: string
  source?: string
}
export type InstructionSection = {
  path: string
  kind: string
  source?: string
  content: string
  truncated: boolean
  original_bytes?: number
  loaded_bytes: number
}
export type ProjectContextResult = {
  root: string
  workspace_id: string
  summary: {
    memory_files: {
      path: string
      kind: string
      source?: string
      truncated: boolean
    }[]
    memory_bytes: number
    instruction_bytes: number
    git: {
      skipped?: boolean
      is_repo: boolean
      branch?: string
      commits: number
    }
    rules: number
    skills: number
  }
  instruction_context: {
    root: string
    workspace_id: string
    instructions_text: string
    instruction_bytes: number
    instruction_truncated?: boolean
    global_context?: string
    global_rules: InstructionRule[]
    rules: InstructionRule[]
    skills: InstructionSkill[]
    sources: InstructionSource[]
    project_memory: {
      sections: InstructionSection[]
      imports?: InstructionSection[]
      total_bytes: number
      budget_bytes: number
      budget_truncated: boolean
    }
    auto_memory: { loaded: boolean; content?: string; bytes: number }
    git: {
      skipped?: boolean
      is_repo: boolean
      root?: string
      branch?: string
      status_short?: string
      recent_commits?: string[]
      error?: string
    }
    environment: Record<string, unknown>
    [key: string]: unknown
  }
}
export type ProjectContextOptions = {
  path?: string
  include_git?: boolean
  include_memory?: boolean
  include_skills?: boolean
}
export type UpstreamAuth = {
  type?: "auto" | "oauth" | "none" | string
  scope?: string
}
export type UpstreamServer = {
  id: string
  name: string
  transport: "http" | "stdio" | string
  enabled: boolean
  command?: string
  args?: string[]
  env?: Record<string, string>
  cwd?: string
  url?: string
  headers?: Record<string, string>
  bearer_token_env_var?: string
  auth?: UpstreamAuth
  tool_prefix?: string
  expose?: "all" | "allowlist" | "meta_only" | "none" | string
  tools?: string[]
  disabled_tools?: string[]
  idle_timeout_sec?: number
}
export type UpstreamServerStatus = {
  id: string
  name: string
  enabled: boolean
  transport: string
  auth: string
  health: "unknown" | "connected" | "unreachable" | "disabled" | string
  connected: boolean
  tool_count: number
  expose: string
  proxied_tools: string[]
  last_error?: string
  pid?: number
}
export type UpstreamServerTools = {
  server_id: string
  tools: Tool[]
  proxied_tools: string[]
}
export type UpstreamOAuthStatus = {
  server_id: string
  configured: boolean
  issuer?: string
  resource?: string
  registration?: string
  client_id?: string
  scopes?: string[]
  has_refresh_token: boolean
  expires_at?: string
  expired: boolean
}
export type UpstreamOAuthLogin = {
  redirect_origin: string
  issuer?: string
  client_id?: string
  client_secret_env_var?: string
  client_metadata_url?: string
  scope?: string
}
export type UpstreamOAuthSession = {
  session_id: string
  authorization_url: string
  expires_at: string
}
export type PublicConfig = {
  http: {
    exposure: {
      mode: "none" | "all" | "0.0.0.0" | "interfaces"
      interfaces: string[]
    }
    security: {
      allow_insecure: boolean
      allow_unauthenticated_loopback: boolean
    }
    mcp: {
      enabled: boolean
      port: number
      auth: {
        enabled: boolean
        legacy_bearer: boolean
        token_configured: boolean
      }
    }
    admin: {
      enabled: boolean
      port: number
      auth: {
        enabled: boolean
        token_configured: boolean
      }
    }
  }
  permissions: { allow_dirs: string[] }
  shell: { path: string[] }
  integrations: {
    ponytail: { active: boolean; mode: "lite" | "full" | "ultra" }
    caveman: {
      active: boolean
      mode:
        | "lite"
        | "full"
        | "ultra"
        | "wenyan-lite"
        | "wenyan-full"
        | "wenyan-ultra"
    }
    rtk: { enabled: boolean; path: string }
    codegraph: { enabled: boolean; path: string }
    typesafe: { enabled: boolean; model: string; timeout_ms: number }
  }
}
export type NetworkAddress = {
  address: string
  interface?: string
  scope: "local" | "lan" | "network" | string
}
export type NetworkInterface = { name: string; addresses: NetworkAddress[] }
export type TunnelAdminScope = {
  organization_id?: string
  workspace_id?: string
  tenant_id?: string
}
export type TunnelConfig = {
  enabled: boolean
  id?: string
  api_key?: string
  runtime_key_configured?: boolean
  runtime_key_preview?: string
  admin_key_configured?: boolean
  admin_key_preview?: string
  admin_organization_id?: string
  admin_workspace_id?: string
  admin_tenant_id?: string
  control_plane_base_url?: string
  organization_id?: string
}
export type TunnelAdminKeyRequest = {
  admin_key?: string
  organization_id?: string
  workspace_id?: string
  tenant_id?: string
}
export type TunnelAdminAccess = { read: boolean; manage: boolean }
export type TunnelAdminKeyStatus = {
  enabled?: boolean
  key_configured?: boolean
  key_preview?: string
  configured: boolean
  verified?: boolean
  scope: TunnelAdminScope
  access: TunnelAdminAccess
  tunnels?: number
}
export type TunnelMetadata = {
  id: string
  name: string
  description: string
  creator?: string
  tenant_ids?: string[]
  workspace_ids?: string[]
  organization_ids?: string[]
  request_id?: string
  fetched_at: string
}
export type ManagedTunnelCreateRequest = {
  name: string
  description: string
  tenant_ids?: string[]
  workspace_ids?: string[]
  organization_ids?: string[]
}
export type ManagedTunnelUpdateRequest = {
  name?: string
  description?: string
  tenant_ids?: string[]
  workspace_ids?: string[]
  organization_ids?: string[]
}
export type ManagedTunnelUseRequest = {
  id: string
  runtime_api_key?: string
  auto_generate_runtime_key?: boolean
  project_id?: string
}
export type ManagedTunnelUseResult = {
  metadata: TunnelMetadata
  status: TunnelStatus
}
export type TunnelStatus = {
  provider: "openai" | string
  enabled: boolean
  running: boolean
  ready: boolean
  restarting: boolean
  id?: string
  control_plane_base_url?: string
  organization_id?: string
  started_at?: string
  last_error?: string
  metadata?: TunnelMetadata
  metadata_error?: string
  admin_key_configured?: boolean
  admin_scope?: TunnelAdminScope
}

export type CFTunnelStatus = {
  version?: string
  platform: string
  source: "system" | "managed" | "unavailable" | string
  path?: string
  verified: boolean
  managed_supported: boolean
  managed_installed: boolean
  consumer: string
}
export type CFTunnelProbeResult = { status: CFTunnelStatus; version?: string }
export type CFTunnelInstallResult = {
  status: CFTunnelStatus
  path?: string
  version?: string
  installed: boolean
  already_installed: boolean
}
export type CFTunnelRemoveResult = { status: CFTunnelStatus; removed: boolean }

export type GlobalExecutableResolution = {
  status: IntegrationStatus
  available: boolean
  path?: string
  managed_recommended: boolean
}

export type ApprovalStatus =
  | "pending"
  | "approved"
  | "denied"
  | "expired"
  | "cancelled"
  | "consumed"
  | string
export type ApprovalRequest = {
  id: string
  status: ApprovalStatus
  workspace_id: string
  source?: string
  target_tool: string
  arguments?: Record<string, unknown>
  digest?: string
  guard_code?: string
  guard_reason?: string
  title: string
  command?: string
  created_at: string
  expires_at: string
  resolved_at?: string
  resolved_by?: string
  reason?: string
  retry_until?: string
  consumed_at?: string
  runtime_session_grant?: boolean
  grant_expires_at?: string
}

export type ApprovalExplainStatus = {
  mode: "off" | "manual" | "auto" | string
  available: boolean
  active_provider?: string
  model?: string
  configured: boolean
  readiness: string
  reason?: string
}

export type ApprovalExplanation = {
  summary: string
  steps?: string[]
  effects?: string[]
  risk_notes?: string[]
  unknowns?: string[]
  provider_id: string
  model: string
  generated_at: string
}

export type ApprovalExplanationResult = {
  request_id: string
  state: "none" | "pending" | "ready" | "failed" | string
  attempt?: number
  explanation?: ApprovalExplanation
  failure?: string
  updated_at?: string
}

export type LLMCredentialState = {
  provider_id: string
  configured: boolean
  preview: string
}

export type LLMProvider = {
  id: string
  name: string
  protocol: string
  base_url: string
  model?: string
  auth_mode: string
  discovery: string
  core_kind?: string
  core: boolean
  selected: boolean
  configured: boolean
  readiness: string
  reason?: string
  credential: LLMCredentialState
}

export type LLMStatus = {
  active_provider: string
  active: LLMProvider
  providers: LLMProvider[]
}

export type LLMModel = {
  id: string
  name?: string
  canonical_slug?: string
  author?: string
  context_length?: number
  context_length_known?: boolean
  prompt_price?: string
  completion_price?: string
  pricing_known?: boolean
  free?: boolean
  free_known?: boolean
  supported_parameters?: string[]
  input_modalities?: string[]
  output_modalities?: string[]
  capabilities?: string[]
  supports_structured_output?: boolean
  created_at?: string
  modified_at?: string
  max_output_tokens?: number
  ollama?: {
    size_bytes?: number
    digest?: string
    format?: string
    family?: string
    families?: string[]
    parameter_size?: string
    parameter_count?: number
    quantization_level?: string
  }
  rank?: { position: number; kind: string; source: string; basis: string; window?: string; freshness?: string; value?: string }
  recommendation?: { position: number; task: string; source: string; basis: string; freshness?: string; share?: number }
}

export type LLMModelSort = { field: string; direction: string }
export type LLMModelQueryCapabilities = {
  filters: string[]
  sorts: string[]
  ranks?: string[]
  rank_windows?: string[]
  recommendation: boolean
}

export type LLMModelCatalog = {
  provider_id: string
  total_catalog: number
  matched: number
  offset: number
  limit: number
  returned: number
  has_more: boolean
  models: LLMModel[]
  refreshed: boolean
  sort: LLMModelSort[]
  rank_source?: string
  rank_window?: string
  rank_basis?: string
  rank_freshness?: string
  recommendation_basis?: string
  recommendation_source?: string
  recommendation_freshness?: string
  access_checked?: boolean
  access_checked_at?: string
  access_available?: number
  access_unavailable?: number
  access_unknown?: number
  access_error?: string
  model_access?: Record<string, {
    state: "available" | "unavailable" | "unknown" | string
    error_category?: string
    reason?: string
    checked_at: string
  }>
  query_capabilities: LLMModelQueryCapabilities
}

export type LLMModelQueryParams = {
  search?: string
  id?: string[]
  free?: boolean
  paid?: boolean
  author?: string[]
  min_context?: number
  max_context?: number
  min_prompt_price?: string
  max_prompt_price?: string
  min_completion_price?: string
  max_completion_price?: string
  capability?: string[]
  parameter?: string[]
  input?: string[]
  output?: string[]
  family?: string[]
  format?: string[]
  quantization?: string[]
  min_parameters?: number
  max_parameters?: number
  min_size?: number
  max_size?: number
  created_after?: string
  created_before?: string
  modified_after?: string
  modified_before?: string
  sort?: string[]
  rank?: string
  window?: string
  recommend_for?: string
  offset?: number
  limit?: number
  range?: string
  count?: boolean
  all?: boolean
  refresh?: boolean
  check_access?: boolean
}

export type LLMProviderConfig = {
  name: string
  protocol: "openai" | "anthropic" | string
  base_url: string
  model: string
  auth_mode: "none" | "bearer" | "x-api-key" | string
  discovery: "none" | "openai-models" | "ollama-tags" | string
}

export type LLMProbeResult = {
  provider_id: string
  model?: string
  readiness: string
}
export type ApprovalEvent = {
  sequence?: number
  name: string
  subject: "challenge" | "request" | "grant"
  request_id?: string
  workspace_id: string
  source?: string
  target_tool: string
  status?: ApprovalStatus
  created_at: string
  expires_at: string
  retry_until?: string
  grant_expires_at?: string
  timestamp: string
}

export type CompletionStatus =
  "completed" | "partial" | "blocked" | "cancelled" | string
export type CompletionRecord = {
  id: string
  sequence: number
  agent_id: string
  workspace_id: string
  status: CompletionStatus
  title: string
  summary: string
  source?: string
  supersedes_id?: string
  created_at: string
}
export type CompletionEvent = {
  id: string
  sequence: number
  name: "completion.accepted" | string
  record: CompletionRecord
  timestamp: string
}
export type CompletionSnapshot = {
  latest_sequence: number
  records: CompletionRecord[]
}

export type StatusOverview = {
  runtime_running: boolean
  mcp_http_enabled: boolean
  admin_enabled: boolean
  tunnel_enabled: boolean
  telegram_enabled: boolean
  telegram_running: boolean
  telegram_healthy: boolean
}

export type AuthStatus = {
  mcp_enabled: boolean
  mcp_configured: boolean
  mcp_legacy_bearer: boolean
  admin_enabled: boolean
  admin_configured: boolean
  unauthenticated_loopback: boolean
  cleartext_http: boolean
}

export type AuthRotationResult = {
  token: string
  status: AuthStatus
}

export type SettingFieldSpec = {
  Key: string
  Label: string
  Description: string
  Kind: string
  Editable: boolean
  Sensitive: boolean
  Writable: boolean
  Secret: boolean
  Clearable: boolean
  Options?: string[]
  Values?: { value: string; description?: string }[]
  Input?: {
    shape?: string
    item_shape?: string
    min_int?: number
    max_int?: number
    has_min_int?: boolean
    has_max_int?: boolean
  }
}

export type SettingResult = {
  Spec: SettingFieldSpec
  Value: string
  Configured?: boolean
  RuntimeReloaded: boolean
}

export type ConfigExportDocument = {
  FileName: string
  Data: string
}

export type TelegramSetupResult = {
  token: SettingResult
  enabled: boolean
  authorized_users: number[]
}
export type TelemetryStatus = {
  persisted_enabled: boolean
  effective_enabled: boolean
  source: string
  environment_override: boolean
  endpoint_available: boolean
  endpoint_host?: string
  product?: string
  identity_present: boolean
}
export type LogEvent = {
  sequence?: number
  run_id?: string
  timestamp?: string
  level?: string
  component?: string
  name?: string
  message?: string
  workspace_id?: string
  tool?: string
  status?: string
  source?: string
  fields?: { key: string; value: unknown }[]
  [key: string]: unknown
}
export type LogsSnapshot = {
  Events?: LogEvent[]
  events?: LogEvent[]
  Session?: string
  session?: string
  Total?: number
  total?: number
  Truncated?: boolean
  truncated?: boolean
}
export type LogsInfo = {
  Path?: string
  path?: string
  Files?: number
  files?: number
  Bytes?: number
  bytes?: number
}
export type ProcessInfo = {
  id: string
  execution_id?: string
  pid: number
  command: string
  cwd: string
  started_at: string
  running: boolean
  exit_code?: number | null
  signal?: string | null
}
export type TypeSafeStatus = {
  enabled: boolean
  api_key_configured: boolean
  state: string
  model: string
  timeout_ms: number
}
export type IntegrationStatus = Record<string, unknown>
export type CodeGraphWorkspaceStatus = Record<string, unknown>

const adminTokenKey = "cm-admin-token"
try {
  localStorage.removeItem(adminTokenKey)
} catch {
  /* storage may be unavailable */
}
export const adminToken = {
  get: () => sessionStorage.getItem(adminTokenKey) ?? "",
  set: (token: string) => sessionStorage.setItem(adminTokenKey, token),
  clear: () => sessionStorage.removeItem(adminTokenKey),
}
export class ApiError extends Error {
  status: number
  constructor(message: string, status: number) {
    super(message)
    this.name = "ApiError"
    this.status = status
  }
}

export function adminRequestHeaders(
  path: string,
  method = "GET",
  initial?: HeadersInit
) {
  const headers = browserOperationHeaders(path, method, initial)
  const token = adminToken.get()
  if (token) headers.set("Authorization", `Bearer ${token}`)
  return headers
}

export async function api<T>(path: string, init?: RequestInit): Promise<T> {
  const headers = adminRequestHeaders(
    path,
    init?.method ?? "GET",
    init?.headers
  )
  if (init?.body !== undefined && init.body !== null)
    headers.set("Content-Type", "application/json")
  const response = await fetch(path, { ...init, headers })
  const text = response.status === 204 ? "" : await response.text()
  if (!response.ok)
    throw new ApiError(text.trim() || `API ${response.status}`, response.status)
  if (!text) return undefined as T
  try {
    return JSON.parse(text) as T
  } catch {
    throw new Error(`Invalid JSON response from ${path}`)
  }
}

export const adminApi = {
  health: () => api<{ ok: boolean; auth_enabled: boolean }>("/api/health"),
  status: () => api<StatusOverview>("/api/status"),
  doctor: () => api<Record<string, unknown>>("/api/doctor"),
  about: () => api<Record<string, unknown>>("/api/about"),
  runtimeAction: (
    action: "up" | "down" | "restart",
    scope: "user" | "system" = "user"
  ) =>
    api<Record<string, unknown>>(`/api/runtime/${action}`, {
      method: "POST",
      body: JSON.stringify({ scope }),
    }),
  logs: (
    options: {
      tail?: number
      all?: boolean
      level?: string
      component?: string
      workspace?: string
      grep?: string
    } = {}
  ) => {
    const query = new URLSearchParams()
    if (options.tail !== undefined) query.set("tail", String(options.tail))
    if (options.all !== undefined) query.set("all", String(options.all))
    if (options.level) query.set("level", options.level)
    if (options.component) query.set("components", options.component)
    if (options.workspace) query.set("workspace", options.workspace)
    if (options.grep) query.set("grep", options.grep)
    const suffix = query.size ? `?${query}` : ""
    return api<LogsSnapshot>(`/api/logs${suffix}`)
  },
  followLogs: (
    options: {
      tail?: number
      all?: boolean
      level?: string
      component?: string
      workspace?: string
      grep?: string
    } = {}
  ) => {
    const query = new URLSearchParams()
    if (options.tail !== undefined) query.set("tail", String(options.tail))
    if (options.all !== undefined) query.set("all", String(options.all))
    if (options.level) query.set("level", options.level)
    if (options.component) query.set("components", options.component)
    if (options.workspace) query.set("workspace", options.workspace)
    if (options.grep) query.set("grep", options.grep)
    const suffix = query.size ? `?${query}` : ""
    return api<LogsSnapshot>(`/api/logs/follow${suffix}`)
  },
  logsInfo: () => api<LogsInfo>("/api/logs/info"),
  clearLogs: () => api<{ cleared: boolean }>("/api/logs", { method: "DELETE" }),
  telemetry: () => api<TelemetryStatus>("/api/telemetry"),
  setTelemetry: (enabled: boolean) =>
    api<TelemetryStatus>(`/api/telemetry/${enabled ? "enable" : "disable"}`, {
      method: "POST",
    }),
  configPath: () => api<Record<string, unknown>>("/api/config/path"),
  verifyConfig: () => api<Record<string, unknown>>("/api/config/verify"),
  authStatus: () => api<AuthStatus>("/api/auth"),
  authAction: (
    scope: "mcp" | "admin",
    action: "rotate" | "enable" | "disable"
  ) =>
    api<AuthStatus | AuthRotationResult>(`/api/auth/${scope}/${action}`, {
      method: "POST",
    }),
  settings: (prefix = "", query = "") => {
    const values = new URLSearchParams()
    if (prefix.trim()) values.set("prefix", prefix.trim())
    if (query.trim()) values.set("query", query.trim())
    return api<SettingResult[]>(`/api/settings${values.size ? `?${values}` : ""}`)
  },
  setting: (key: string) =>
    api<SettingResult>(`/api/settings/${encodeURIComponent(key)}`),
  setSetting: (key: string, value: string, action = "set") =>
    api<SettingResult>(`/api/settings/${encodeURIComponent(key)}`, {
      method: "PUT",
      body: JSON.stringify({
        Action: action,
        Value: value,
        SecretSource: "browser-protected-input",
      }),
    }),
  exportSettings: () => api<ConfigExportDocument>("/api/settings/export"),
  notificationStatus: () =>
    api<Record<string, unknown>>("/api/notifications"),
  telegramSetup: (token: string, userID: number) =>
    api<TelegramSetupResult>("/api/telegram/setup", {
      method: "PUT",
      body: JSON.stringify({ token, user_id: userID }),
    }),
  rtkStatus: () => api<IntegrationStatus>("/api/integrations/rtk"),
  rtkAction: (action: "enable" | "disable" | "probe" | "install") =>
    api<IntegrationStatus>(`/api/integrations/rtk/${action}`, {
      method: "POST",
    }),
  rtkGlobal: () => api<GlobalExecutableResolution>("/api/integrations/rtk/global"),
  codeGraphStatus: () => api<IntegrationStatus>("/api/integrations/codegraph"),
  codeGraphAction: (action: "probe" | "install") =>
    api<IntegrationStatus>(`/api/integrations/codegraph/${action}`, {
      method: "POST",
    }),
  codeGraphGlobal: () => api<GlobalExecutableResolution>("/api/integrations/codegraph/global"),
  typeSafeStatus: () => api<TypeSafeStatus>("/api/integrations/typesafe"),
  typeSafeAction: (action: "enable" | "disable" | "probe") =>
    api<TypeSafeStatus | Record<string, unknown>>(
      `/api/integrations/typesafe/${action}`,
      { method: "POST" }
    ),
  activityCall: (callID: string) =>
    api<ToolCallDetail>(`/api/activity/${encodeURIComponent(callID)}`),
  networkInterfaces: () => api<NetworkInterface[]>("/api/network/interfaces"),
  config: () => api<PublicConfig>("/api/config"),
  saveConfig: (config: Partial<PublicConfig>) =>
    api<PublicConfig>("/api/config", {
      method: "PUT",
      body: JSON.stringify(config),
    }),
  workspaces: () => api<Workspace[]>("/api/workspaces"),
  workspace: (id: string) =>
    api<Workspace>(`/api/workspaces/${encodeURIComponent(id)}`),
  registerWorkspace: (path: string) =>
    api<Workspace>("/api/workspaces", {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  removeWorkspace: (id: string) =>
    api<void>(`/api/workspaces/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  purgeWorkspace: (id: string) =>
    api<Workspace>(`/api/workspaces/${encodeURIComponent(id)}/purge`, {
      method: "POST",
      body: JSON.stringify({ confirm: true }),
    }),
  relocateWorkspace: (id: string, path: string, resolution = "") =>
    api<Workspace>(`/api/workspaces/${encodeURIComponent(id)}/relocate`, {
      method: "POST",
      body: JSON.stringify({ path, resolution }),
    }),
  workspaceAccess: (id: string) =>
    api<string[]>(`/api/workspaces/${encodeURIComponent(id)}/access`),
  addWorkspaceAccess: (id: string, path: string) =>
    api<Workspace>(`/api/workspaces/${encodeURIComponent(id)}/access`, {
      method: "POST",
      body: JSON.stringify({ path }),
    }),
  removeWorkspaceAccess: (id: string, path: string) =>
    api<Workspace>(`/api/workspaces/${encodeURIComponent(id)}/access`, {
      method: "DELETE",
      body: JSON.stringify({ path }),
    }),
  workspaceContainers: () =>
    api<WorkspaceContainer[]>("/api/workspace-containers"),
  workspaceContainer: (id: string) =>
    api<WorkspaceContainer>(
      `/api/workspace-containers/${encodeURIComponent(id)}`
    ),
  createWorkspaceContainer: (name: string) =>
    api<WorkspaceContainer>("/api/workspace-containers", {
      method: "POST",
      body: JSON.stringify({ name }),
    }),
  renameWorkspaceContainer: (id: string, name: string) =>
    api<WorkspaceContainer>(
      `/api/workspace-containers/${encodeURIComponent(id)}`,
      { method: "PATCH", body: JSON.stringify({ name }) }
    ),
  removeWorkspaceContainer: (id: string) =>
    api<void>(`/api/workspace-containers/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  workspaceContainersForWorkspace: (id: string) =>
    api<WorkspaceContainer[]>(
      `/api/workspaces/${encodeURIComponent(id)}/containers`
    ),
  addWorkspaceContainers: (id: string, containerIDs: string[]) =>
    api<WorkspaceContainer[]>(
      `/api/workspaces/${encodeURIComponent(id)}/containers`,
      { method: "POST", body: JSON.stringify({ container_ids: containerIDs }) }
    ),
  removeWorkspaceContainers: (id: string, containerIDs: string[]) =>
    api<WorkspaceContainer[]>(
      `/api/workspaces/${encodeURIComponent(id)}/containers`,
      {
        method: "DELETE",
        body: JSON.stringify({ container_ids: containerIDs }),
      }
    ),
  globalInstructions: () => api<GlobalInstructions>("/api/instructions/global"),
  prompts: (workspaceID = "") =>
    api<ScopedPrompt[]>(
      `/api/prompts?workspace_id=${encodeURIComponent(workspaceID)}`
    ),
  prompt: (name: string, workspaceID = "") =>
    api<ScopedPrompt>(
      `/api/prompts/${encodeURIComponent(name)}?workspace_id=${encodeURIComponent(workspaceID)}`
    ),
  createPrompt: (
    scope: "global" | "workspace",
    workspaceID: string,
    definition: PromptDefinition
  ) =>
    api<ScopedPrompt>("/api/prompts", {
      method: "POST",
      body: JSON.stringify({ scope, workspace_id: workspaceID, definition }),
    }),
  updatePrompt: (
    name: string,
    workspaceID: string,
    definition: PromptDefinition
  ) =>
    api<ScopedPrompt>(
      `/api/prompts/${encodeURIComponent(name)}?workspace_id=${encodeURIComponent(workspaceID)}`,
      { method: "PUT", body: JSON.stringify(definition) }
    ),
  deletePrompt: (name: string, workspaceID: string) =>
    api<{ deleted: boolean }>(
      `/api/prompts/${encodeURIComponent(name)}?workspace_id=${encodeURIComponent(workspaceID)}`,
      { method: "DELETE" }
    ),
  saveGlobalInstructions: (
    patch: Partial<
      Pick<GlobalInstructions, "context" | "rules" | "source_policy">
    >
  ) =>
    api<GlobalInstructions>("/api/instructions/global", {
      method: "PUT",
      body: JSON.stringify(patch),
    }),
  workspaceContext: (id: string, options: ProjectContextOptions = {}) => {
    const query = new URLSearchParams()
    if (options.path?.trim()) query.set("path", options.path.trim())
    if (options.include_git !== undefined)
      query.set("include_git", String(options.include_git))
    if (options.include_memory !== undefined)
      query.set("include_memory", String(options.include_memory))
    if (options.include_skills !== undefined)
      query.set("include_skills", String(options.include_skills))
    const suffix = query.size ? `?${query}` : ""
    return api<ProjectContextResult>(
      `/api/workspaces/${encodeURIComponent(id)}/context${suffix}`
    )
  },
  workspaceExecutions: (id: string, limit = 50) =>
    api<ExecutionInfo[]>(
      `/api/workspaces/${encodeURIComponent(id)}/executions?limit=${limit}`
    ),
  workspaceExecution: (id: string, executionID: string) =>
    api<ExecutionSnapshot>(
      `/api/workspaces/${encodeURIComponent(id)}/executions/${encodeURIComponent(executionID)}`
    ),
  workspaceProcesses: (id: string) =>
    api<ProcessInfo[]>(`/api/workspaces/${encodeURIComponent(id)}/processes`),
  workspaceProcess: (id: string, processID: string) =>
    api<ProcessInfo>(
      `/api/workspaces/${encodeURIComponent(id)}/processes/${encodeURIComponent(processID)}`
    ),
  clearWorkspaceProcess: (id: string, processID: string) =>
    api<{ deleted: boolean }>(
      `/api/workspaces/${encodeURIComponent(id)}/processes/${encodeURIComponent(processID)}`,
      { method: "DELETE" }
    ),
  codeGraphWorkspace: (id: string, path = "") =>
    api<CodeGraphWorkspaceStatus>(
      `/api/workspaces/${encodeURIComponent(id)}/integrations/codegraph${path ? `?path=${encodeURIComponent(path)}` : ""}`
    ),
  codeGraphWorkspaceAction: (id: string, action: "init" | "sync", path = "") =>
    api<CodeGraphWorkspaceStatus>(
      `/api/workspaces/${encodeURIComponent(id)}/integrations/codegraph/${action}${path ? `?path=${encodeURIComponent(path)}` : ""}`,
      { method: "POST" }
    ),
  tools: () => api<Tool[]>("/api/tools"),
  upstream: () => api<UpstreamServer[]>("/api/upstream"),
  upstreamServer: (id: string) =>
    api<UpstreamServer>(`/api/upstream/${encodeURIComponent(id)}`),
  addUpstream: (server: UpstreamServer) =>
    api<UpstreamServer>("/api/upstream", {
      method: "POST",
      body: JSON.stringify(server),
    }),
  updateUpstream: (id: string, server: UpstreamServer) =>
    api<UpstreamServer>(`/api/upstream/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(server),
    }),
  removeUpstream: (id: string) =>
    api<void>(`/api/upstream/${encodeURIComponent(id)}`, { method: "DELETE" }),
  setUpstreamEnabled: (id: string, enabled: boolean) =>
    api<UpstreamServer>(
      `/api/upstream/${encodeURIComponent(id)}/${enabled ? "enable" : "disable"}`,
      { method: "POST" }
    ),
  upstreamStatus: (id: string, refresh = true) =>
    api<UpstreamServerStatus>(
      `/api/upstream/${encodeURIComponent(id)}/status?refresh=${refresh}`
    ),
  upstreamTools: (id: string, refresh = false) =>
    api<UpstreamServerTools>(
      `/api/upstream/${encodeURIComponent(id)}/tools?refresh=${refresh}`
    ),
  upstreamOAuthStatus: (id: string) =>
    api<UpstreamOAuthStatus>(
      `/api/upstream/${encodeURIComponent(id)}/auth/status`
    ),
  beginUpstreamOAuth: (id: string, request: UpstreamOAuthLogin) =>
    api<UpstreamOAuthSession>(
      `/api/upstream/${encodeURIComponent(id)}/auth/login`,
      { method: "POST", body: JSON.stringify(request) }
    ),
  logoutUpstreamOAuth: (id: string) =>
    api<void>(`/api/upstream/${encodeURIComponent(id)}/auth/logout`, {
      method: "DELETE",
    }),
  tunnel: () => api<TunnelStatus>("/api/tunnel"),
  syncTunnel: () =>
    api<TunnelStatus>("/api/tunnel/sync", { method: "POST" }),
  cfTunnel: () => api<CFTunnelStatus>("/api/integrations/cf"),
  probeCFTunnel: () =>
    api<CFTunnelProbeResult>("/api/integrations/cf/probe", { method: "POST" }),
  installCFTunnel: () =>
    api<CFTunnelInstallResult>("/api/integrations/cf/install", { method: "POST" }),
  updateCFTunnel: () =>
    api<CFTunnelInstallResult>("/api/integrations/cf/update", { method: "POST" }),
  removeCFTunnel: () =>
    api<CFTunnelRemoveResult>("/api/integrations/cf", { method: "DELETE" }),
  tunnelConfig: () => api<TunnelConfig>("/api/tunnel/config"),
  clearTunnelRuntimeKey: () => api<unknown>("/api/tunnel/runtime/key", { method: "DELETE" }),
  configureTunnel: (config: TunnelConfig) =>
    api<TunnelStatus>("/api/tunnel", {
      method: "PUT",
      body: JSON.stringify(config),
    }),
  tunnelAdminKey: () => api<TunnelAdminKeyStatus>("/api/tunnel/admin/key"),
  configureTunnelAdminKey: (request: TunnelAdminKeyRequest) =>
    api<TunnelAdminKeyStatus>("/api/tunnel/admin/key", {
      method: "PUT",
      body: JSON.stringify(request),
    }),
  verifyTunnelAdminKey: () =>
    api<TunnelAdminKeyStatus>("/api/tunnel/admin/key", { method: "POST" }),
  removeTunnelAdminKey: () =>
    api<TunnelAdminKeyStatus>("/api/tunnel/admin/key", { method: "DELETE" }),
  managedTunnels: () => api<TunnelMetadata[]>("/api/tunnel/managed"),
  managedTunnel: (id: string) =>
    api<TunnelMetadata>(`/api/tunnel/managed/${encodeURIComponent(id)}`),
  createManagedTunnel: (request: ManagedTunnelCreateRequest) =>
    api<TunnelMetadata>("/api/tunnel/managed", {
      method: "POST",
      body: JSON.stringify(request),
    }),
  updateManagedTunnel: (id: string, request: ManagedTunnelUpdateRequest) =>
    api<TunnelMetadata>(`/api/tunnel/managed/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify(request),
    }),
  deleteManagedTunnel: (id: string) =>
    api<TunnelMetadata>(`/api/tunnel/managed/${encodeURIComponent(id)}`, {
      method: "DELETE",
    }),
  useManagedTunnel: (request: ManagedTunnelUseRequest) =>
    api<ManagedTunnelUseResult>("/api/tunnel/managed/use", {
      method: "POST",
      body: JSON.stringify(request),
    }),
  startTunnel: () => api<TunnelStatus>("/api/tunnel", { method: "POST" }),
  stopTunnel: () => api<TunnelStatus>("/api/tunnel", { method: "DELETE" }),
  approvalRequests: (status = "pending", workspaceID = "") => {
    const query = new URLSearchParams({ status })
    if (workspaceID) query.set("workspace_id", workspaceID)
    return api<ApprovalRequest[]>(`/api/requests?${query}`)
  },
  approvalRequest: (id: string) =>
    api<ApprovalRequest>(`/api/requests/${encodeURIComponent(id)}`),
  approveRequest: (id: string, reason = "") =>
    api<ApprovalRequest>(`/api/requests/${encodeURIComponent(id)}/approve`, {
      method: "POST",
      body: JSON.stringify({ reason }),
    }),
  denyRequest: (id: string, reason = "") =>
    api<ApprovalRequest>(`/api/requests/${encodeURIComponent(id)}/deny`, {
      method: "POST",
      body: JSON.stringify({ reason }),
    }),
  approvalExplainStatus: () =>
    api<ApprovalExplainStatus>("/api/requests/explain/status"),
  approvalExplanation: (id: string) =>
    api<ApprovalExplanationResult>(
      `/api/requests/${encodeURIComponent(id)}/explanation`
    ),
  explainApproval: (id: string, retry = false) =>
    api<ApprovalExplanationResult>(
      `/api/requests/${encodeURIComponent(id)}/explain`,
      { method: "POST", body: JSON.stringify({ retry }) }
    ),
  approvalGrants: (workspaceID = "") => {
    const query = new URLSearchParams()
    if (workspaceID) query.set("workspace_id", workspaceID)
    return api<ApprovalRequest[]>(
      `/api/requests/grants${query.size ? `?${query}` : ""}`
    )
  },
  revokeApprovalGrant: (id: string) =>
    api<ApprovalRequest>(
      `/api/requests/grants/${encodeURIComponent(id)}/revoke`,
      { method: "POST" }
    ),
  llmStatus: () => api<LLMStatus>("/api/llm/status"),
  llmProviders: () => api<LLMProvider[]>("/api/llm/providers"),
  llmProvider: (id: string) =>
    api<LLMProvider>(`/api/llm/providers/${encodeURIComponent(id)}`),
  addLLMProvider: (id: string, config: LLMProviderConfig) =>
    api<LLMProvider>("/api/llm/providers", {
      method: "POST",
      body: JSON.stringify({ id, config }),
    }),
  configureLLMProvider: (id: string, config: LLMProviderConfig) =>
    api<LLMProvider>(`/api/llm/providers/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify({ config }),
    }),
  setLLMProviderModel: (id: string, model: string) =>
    api<LLMProvider>(`/api/llm/providers/${encodeURIComponent(id)}`, {
      method: "PUT",
      body: JSON.stringify({ model }),
    }),
  removeLLMProvider: (id: string) =>
    api<{ provider_id: string; removed: boolean }>(
      `/api/llm/providers/${encodeURIComponent(id)}`,
      { method: "DELETE" }
    ),
  selectLLMProvider: (id: string) =>
    api<LLMProvider>(`/api/llm/providers/${encodeURIComponent(id)}/select`, {
      method: "POST",
    }),
  llmModels: (id: string, query: LLMModelQueryParams = {}) => {
    const values = new URLSearchParams()
    for (const [key, value] of Object.entries(query)) {
      if (value === undefined || value === "" || value === false) continue
      if (Array.isArray(value)) value.forEach((item) => values.append(key, String(item)))
      else values.set(key, String(value))
    }
    const suffix = values.size ? `?${values.toString()}` : ""
    return api<LLMModelCatalog>(`/api/llm/providers/${encodeURIComponent(id)}/models${suffix}`)
  },
  probeLLMProvider: (id: string) =>
    api<LLMProbeResult>(`/api/llm/providers/${encodeURIComponent(id)}/probe`, {
      method: "POST",
    }),
  setLLMCredential: (id: string, apiKey: string) =>
    api<LLMCredentialState>(
      `/api/llm/providers/${encodeURIComponent(id)}/credential`,
      { method: "PUT", body: JSON.stringify({ api_key: apiKey }) }
    ),
  clearLLMCredential: (id: string) =>
    api<LLMCredentialState>(
      `/api/llm/providers/${encodeURIComponent(id)}/credential`,
      { method: "DELETE" }
    ),
  completions: (workspaceID = "", limit = 50) => {
    const query = new URLSearchParams({ limit: String(limit) })
    if (workspaceID) query.set("workspace_id", workspaceID)
    return api<CompletionRecord[]>(`/api/completions?${query}`)
  },
  currentCompletion: (workspaceID: string) =>
    api<CompletionRecord>(
      `/api/completions/current?workspace_id=${encodeURIComponent(workspaceID)}`
    ),
  completion: (id: string) =>
    api<CompletionRecord>(`/api/completions/view/${encodeURIComponent(id)}`),
  completionDoctor: (workspaceID = "") =>
    api<Record<string, unknown>>(
      `/api/completions/doctor${workspaceID ? `?workspace_id=${encodeURIComponent(workspaceID)}` : ""}`
    ),
}
