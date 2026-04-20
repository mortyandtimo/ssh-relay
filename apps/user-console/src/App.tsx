import { startTransition, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { MouseEvent } from "react";
import { listen } from "@tauri-apps/api/event";
import { createDesktopApi } from "../../../packages/desktop-core/src/api";
import type { AuthSettings, UserRole, UserServiceEntry, UserSummary } from "../../../packages/desktop-core/src/types";
import {
  appExit,
  createTauriDesktopTransport,
  decryptLoginPassword,
  deleteLoginProfile,
  loadAppConfig,
  loadAutoStartEnabled,
  loadP2PRuntimeStatus,
  loadUserNodeStatus,
  loadDesktopHostPaths,
  openP2PRuntimeLog,
  openServiceWorkspace,
  openServiceWorkspaceExternal,
  probeServiceWorkspace,
  readLoginProfiles,
  saveAppConfig,
  saveLoginProfile,
  setAutoStart,
  startP2PRuntime,
  stopP2PRuntime,
  syncUserNode,
  windowMinimize,
  windowRequestClose,
  windowStartDrag,
  windowToggleMaximize,
  type AppConfig,
  type LoginProfilesFile,
  type P2PRuntimeStatus,
  type UserNodeStatus,
} from "./desktopHost";

type CloseAction = "ask" | "tray" | "exit";
type DriveFallbackPolicy = "admin_only" | "never";
type ImageBulkUploadMode = "p2p_bulk_https_light" | "https_only";
type AuthMode = "login" | "register";
type Page = "loading" | "login" | "home" | "drive" | "gallery" | "p2p" | "management" | "settings";
type ReopenDialogChoice = "cancel" | "new-window";
type ServiceBinding = {
  key: string;
  title: string;
  kind: string;
  summary: string;
  source: "manual" | "catalog" | "catalog+manual";
  publicUrl: string;
  p2pUrl: string;
  cloudAllowed: boolean;
  p2pAllowed: boolean;
  cloudAccess: string;
  p2pAccess: string;
  preferredPath: string;
  nodeName: string;
  nodeStatus: string;
  tunnelName: string;
  tunnelType: string;
  transportPolicy: string;
  runtimePath: string;
  runtimeState: string;
  healthStatus: string;
};

const defaultApiUrl = "https://manage.020309.top";
const defaultP2PPeerUrl = "tcp://easytier.manage.020309.top:11010";
const defaultAuthSettings: AuthSettings = {
  publicRegistrationEnabled: true,
};
const defaultConfig: Required<Pick<AppConfig, "closeAction" | "silentStart" | "autoStart" | "driveFallbackPolicy" | "imageBulkUploadMode" | "p2pUseDhcp">> = {
  closeAction: "ask",
  silentStart: false,
  autoStart: false,
  driveFallbackPolicy: "admin_only",
  imageBulkUploadMode: "p2p_bulk_https_light",
  p2pUseDhcp: true,
};

function normalizeApiBaseUrl(value?: string) {
  return (value || "").trim().replace(/\/+$/, "") || defaultApiUrl;
}

function optionalTrimmed(value?: string) {
  return (value || "").trim();
}

function optionalUrl(value?: string) {
  const trimmed = (value || "").trim();
  return trimmed || "";
}

function normalizePeerUrl(value?: string) {
  if (typeof value !== "string") return defaultP2PPeerUrl;
  return value.trim();
}

function hostFromUrl(value?: string) {
  const trimmed = (value || "").trim();
  if (!trimmed) return "";
  try {
    return new URL(trimmed).host;
  } catch {
    return "";
  }
}

function mergeConfig(config?: AppConfig | null): AppConfig {
  return {
    apiBaseUrl: normalizeApiBaseUrl(config?.apiBaseUrl),
    closeAction: (config?.closeAction || defaultConfig.closeAction) as CloseAction,
    silentStart: Boolean(config?.silentStart),
    autoStart: Boolean(config?.autoStart),
    p2pAutoStart: Boolean(config?.p2pAutoStart),
    driveFallbackPolicy: (config?.driveFallbackPolicy || defaultConfig.driveFallbackPolicy) as DriveFallbackPolicy,
    imageBulkUploadMode: (config?.imageBulkUploadMode || defaultConfig.imageBulkUploadMode) as ImageBulkUploadMode,
    driveCloudUrl: optionalUrl(config?.driveCloudUrl),
    driveP2pUrl: optionalUrl(config?.driveP2pUrl),
    galleryCloudUrl: optionalUrl(config?.galleryCloudUrl),
    galleryP2pUrl: optionalUrl(config?.galleryP2pUrl),
    p2pNetworkName: optionalTrimmed(config?.p2pNetworkName),
    p2pNetworkSecret: optionalTrimmed(config?.p2pNetworkSecret),
    p2pPeerUrl: normalizePeerUrl(config?.p2pPeerUrl),
    p2pVirtualIpv4: optionalTrimmed(config?.p2pVirtualIpv4),
    p2pUseDhcp: config?.p2pUseDhcp !== false,
    p2pInstanceName: optionalTrimmed(config?.p2pInstanceName),
    p2pHostname: optionalTrimmed(config?.p2pHostname),
  };
}

function formatStartedAt(timestamp?: number | null) {
  if (!timestamp) return "未启动";
  return new Date(timestamp).toLocaleString();
}

function formatObservedAt(timestamp?: number | null) {
  if (!timestamp) return "未记录";
  return new Date(timestamp).toLocaleString();
}

function p2pRuntimeLabel(status?: P2PRuntimeStatus | null) {
  if (!status) return "读取中";
  if (status.running && status.peerCount > 0) return `已联网 (${status.peerCount})`;
  if (status.running) return "运行中";
  if (!status.available) return "未打包 easytier-core";
  if (!status.configured) return "待配置";
  return "已停止";
}

function userNodeStatusLabel(status?: UserNodeStatus | null) {
  if (!status) return "读取中";
  if (status.online) return "已注册在线";
  if (status.registered) return "已注册";
  if (status.lastError) return "同步异常";
  return "未注册";
}

function roleLabel(role?: string) {
  switch (role) {
    case "admin":
      return "超级管理员";
    case "manager":
      return "普通管理员";
    case "user":
      return "普通用户";
    default:
      return "未登录";
  }
}

function isSuperAdmin(user?: UserSummary | null) {
  return user?.role === "admin";
}

function isOrdinaryAdmin(user?: UserSummary | null) {
  return user?.role === "manager";
}

function canToggleUserDisabled(actor?: UserSummary | null, target?: UserSummary | null) {
  if (!actor || !target || actor.id === target.id) {
    return false;
  }
  if (actor.role === "admin") {
    return target.role !== "admin";
  }
  if (actor.role === "manager") {
    return target.role === "user";
  }
  return false;
}

function canEditUserRole(actor?: UserSummary | null, target?: UserSummary | null) {
  if (!actor || !target || actor.id === target.id) {
    return false;
  }
  return actor.role === "admin" && target.role !== "admin";
}

function userStateLabel(user?: UserSummary | null) {
  return user?.disabled ? "已封禁" : "正常";
}

function serviceAccessLabel(access?: string) {
  switch (access) {
    case "admin_only":
      return "仅管理员";
    case "disabled":
      return "已禁用";
    default:
      return "全部用户";
  }
}

function preferredPathLabel(value?: string) {
  switch (value) {
    case "p2p":
      return "优先 P2P";
    case "cloud":
      return "优先云端";
    case "dual":
      return "双入口";
    default:
      return "未指定";
  }
}

function serviceSourceLabel(source: ServiceBinding["source"]) {
  switch (source) {
    case "manual":
      return "本地手填";
    case "catalog+manual":
      return "云端目录 + 本地覆盖";
    default:
      return "云端目录";
  }
}

function nodeStatusLabel(status?: string) {
  if (status === "online") return "在线";
  if (status === "offline") return "离线";
  return status || "未知";
}

function healthStatusLabel(status?: string) {
  switch (status) {
    case "healthy":
      return "正常";
    case "node_offline":
      return "节点离线";
    case "capability_missing":
      return "能力缺失";
    case "misconfigured":
      return "配置异常";
    case "target_unreachable":
      return "目标不可达";
    default:
      return status || "未上报";
  }
}

function firstCatalogEntry(items: UserServiceEntry[] | undefined) {
  return items && items.length > 0 ? items[0] : null;
}

function resolveServiceBinding(
  key: string,
  fallbackTitle: string,
  fallbackSummary: string,
  catalogItems: UserServiceEntry[] | undefined,
  manualCloudUrl?: string,
  manualP2pUrl?: string,
): ServiceBinding | null {
  const entry = firstCatalogEntry(catalogItems);
  const manualCloud = optionalUrl(manualCloudUrl);
  const manualP2p = optionalUrl(manualP2pUrl);
  const publicUrl = manualCloud || entry?.publicUrl || "";
  const p2pUrl = manualP2p || entry?.p2pUrl || "";
  if (!entry && !publicUrl && !p2pUrl) {
    return null;
  }
  return {
    key,
    title: entry?.title || fallbackTitle,
    kind: entry?.kind || key,
    summary: entry?.summary || fallbackSummary,
    source: entry ? ((manualCloud || manualP2p) ? "catalog+manual" : "catalog") : "manual",
    publicUrl,
    p2pUrl,
    cloudAllowed: entry ? entry.cloudAllowed : Boolean(publicUrl),
    p2pAllowed: entry ? entry.p2pAllowed : Boolean(p2pUrl),
    cloudAccess: entry?.cloudAccess || (publicUrl ? "all_users" : "disabled"),
    p2pAccess: entry?.p2pAccess || (p2pUrl ? "all_users" : "disabled"),
    preferredPath: entry?.preferredPath || ((publicUrl && p2pUrl) ? "dual" : (p2pUrl ? "p2p" : "cloud")),
    nodeName: entry?.nodeName || "",
    nodeStatus: entry?.nodeStatus || "",
    tunnelName: entry?.tunnelName || "",
    tunnelType: entry?.tunnelType || "",
    transportPolicy: entry?.transportPolicy || "",
    runtimePath: entry?.runtimePath || "",
    runtimeState: entry?.runtimeState || "",
    healthStatus: entry?.healthStatus || "",
  };
}

export default function App() {
  const [page, setPage] = useState<Page>("loading");
  const [user, setUser] = useState<UserSummary | null>(null);
  const [initializing, setInitializing] = useState(true);
  const [busy, setBusy] = useState(false);
  const [p2pBusy, setP2PBusy] = useState(false);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");
  const [closeDialogOpen, setCloseDialogOpen] = useState(false);
  const [reopenDialogOpen, setReopenDialogOpen] = useState(false);
  const [deleteConfirmTarget, setDeleteConfirmTarget] = useState<string | null>(null);
  const [loginProfiles, setLoginProfiles] = useState<LoginProfilesFile | null>(null);
  const [profileListOpen, setProfileListOpen] = useState(false);
  const [appConfig, setAppConfig] = useState<AppConfig>(mergeConfig(null));
  const [desktopHostPaths, setDesktopHostPaths] = useState({
    configDir: "未接入 Tauri 宿主",
    logDir: "未接入 Tauri 宿主",
    available: false,
  });
  const [p2pStatus, setP2PStatus] = useState<P2PRuntimeStatus | null>(null);
  const [userNodeStatus, setUserNodeStatus] = useState<UserNodeStatus | null>(null);
  const [userServices, setUserServices] = useState<UserServiceEntry[]>([]);
  const [serviceLoading, setServiceLoading] = useState(false);
  const [managedUsers, setManagedUsers] = useState<UserSummary[]>([]);
  const [authSettings, setAuthSettings] = useState<AuthSettings>(defaultAuthSettings);
  const [adminBusyAction, setAdminBusyAction] = useState("");
  const [adminLoading, setAdminLoading] = useState(false);
  const [managedUserForm, setManagedUserForm] = useState({
    email: "",
    displayName: "",
    password: "",
    role: "user" as UserRole,
  });
  const [userRoleDrafts, setUserRoleDrafts] = useState<Record<string, UserRole>>({});
  const [passwordChangeForm, setPasswordChangeForm] = useState({
    code: "",
    password: "",
    confirmPassword: "",
  });

  const [apiUrl, setApiUrl] = useState(defaultApiUrl);
  const [authMode, setAuthMode] = useState<AuthMode>("login");
  const [loginEmail, setLoginEmail] = useState("");
  const [loginPassword, setLoginPassword] = useState("");
  const [registerDisplayName, setRegisterDisplayName] = useState("");
  const [registerConfirmPassword, setRegisterConfirmPassword] = useState("");
  const [savePassword, setSavePassword] = useState(false);
  const [autoLogin, setAutoLogin] = useState(false);
  const bootstrapStartedRef = useRef(false);

  const transport = useMemo(() => createTauriDesktopTransport(), []);
  const api = useMemo(() => createDesktopApi(apiUrl, transport || undefined), [apiUrl, transport]);

  const refreshLoginProfiles = useCallback(async () => {
    const profiles = await readLoginProfiles();
    setLoginProfiles(profiles);
    return profiles;
  }, []);

  const persistConfig = useCallback(async (nextConfig: AppConfig) => {
    const merged = mergeConfig(nextConfig);
    setAppConfig(merged);
    setApiUrl(merged.apiBaseUrl || defaultApiUrl);
    await saveAppConfig(merged);
  }, []);

  const refreshP2PStatus = useCallback(async () => {
    const status = await loadP2PRuntimeStatus();
    setP2PStatus(status);
    return status;
  }, []);

  const refreshUserNodeStatus = useCallback(async () => {
    const status = await loadUserNodeStatus();
    setUserNodeStatus(status);
    return status;
  }, []);

  const syncUserNodePresence = useCallback(async (identity?: { email?: string; role?: string; displayName?: string }) => {
    const status = await syncUserNode(identity);
    setUserNodeStatus(status);
    return status;
  }, []);

  const refreshUserServices = useCallback(async (client = api) => {
    setServiceLoading(true);
    try {
      const response = await client.loadUserServices();
      setUserServices(response.items || []);
      return response.items || [];
    } catch {
      setUserServices([]);
      return [];
    } finally {
      setServiceLoading(false);
    }
  }, [api]);

  const refreshAdminData = useCallback(async (client = api, currentUser = user) => {
    if (!currentUser || (currentUser.role !== "admin" && currentUser.role !== "manager")) {
      setManagedUsers([]);
      setUserRoleDrafts({});
      setAuthSettings(defaultAuthSettings);
      return;
    }
    setAdminLoading(true);
    try {
      const [usersPayload, authSettingsPayload] = await Promise.all([
        client.loadManagedUsers(),
        currentUser.role === "admin" ? client.loadAuthSettings() : Promise.resolve(defaultAuthSettings),
      ]);
      setManagedUsers(usersPayload.items || []);
      setAuthSettings(authSettingsPayload);
      setUserRoleDrafts((current) => {
        const next: Record<string, UserRole> = {};
        for (const item of usersPayload.items || []) {
          next[item.id] = current[item.id] || item.role;
        }
        return next;
      });
    } catch (adminError) {
      setManagedUsers([]);
      setUserRoleDrafts({});
      setAuthSettings(defaultAuthSettings);
      setError(adminError instanceof Error ? adminError.message : "加载管理员数据失败");
    } finally {
      setAdminLoading(false);
    }
  }, [api, user]);

  const fillProfileCredentials = useCallback(async (email: string, profiles?: LoginProfilesFile | null) => {
    const activeProfiles = profiles ?? loginProfiles;
    const profile = activeProfiles?.profiles.find((item) => item.email === email);
    setLoginEmail(email);
    setProfileListOpen(false);
    if (!profile) {
      setLoginPassword("");
      setSavePassword(false);
      setAutoLogin(false);
      return;
    }
    try {
      const password = await decryptLoginPassword(email);
      setLoginPassword(password);
      setSavePassword(Boolean(password));
      setAutoLogin(Boolean(password) && profile.autoLogin);
    } catch {
      setLoginPassword("");
      setSavePassword(false);
      setAutoLogin(false);
    }
  }, [loginProfiles]);

  const loadCurrentSession = useCallback(async () => {
    try {
      const response = await api.loadCurrentUser();
      setUser(response.user);
      setPage("home");
      setError("");
      await refreshUserServices(api);
      await refreshAdminData(api, response.user);
      void syncUserNodePresence({
        email: response.user.email,
        role: response.user.role,
        displayName: response.user.displayName,
      });
      return true;
    } catch {
      setUser(null);
      setUserServices([]);
      setManagedUsers([]);
      setUserRoleDrafts({});
      setAuthSettings(defaultAuthSettings);
      return false;
    }
  }, [api, refreshAdminData, refreshUserServices, syncUserNodePresence]);

  useEffect(() => {
    if (bootstrapStartedRef.current) return;
    bootstrapStartedRef.current = true;
    let cancelled = false;
    void (async () => {
      try {
        const [config, profiles, hostPaths, autoStartEnabled] = await Promise.all([
          loadAppConfig(),
          readLoginProfiles(),
          loadDesktopHostPaths(),
          transport ? loadAutoStartEnabled() : Promise.resolve(false),
        ]);
        if (cancelled) return;

        const mergedConfig = {
          ...mergeConfig(config),
          autoStart: transport ? autoStartEnabled : Boolean(config?.autoStart),
        };
        const nextApiUrl = mergedConfig.apiBaseUrl || defaultApiUrl;
        const lastEmail = profiles?.lastUsedEmail || profiles?.profiles[0]?.email || "";
        const lastProfile = lastEmail ? profiles?.profiles.find((item) => item.email === lastEmail) : null;
        const rememberedPasswordPromise = lastProfile
          ? decryptLoginPassword(lastEmail).catch(() => "")
          : Promise.resolve("");

        startTransition(() => {
          setAppConfig(mergedConfig);
          setApiUrl(nextApiUrl);
          setLoginProfiles(profiles);
          setDesktopHostPaths(hostPaths);
          setLoginEmail(lastEmail);
        });
        void refreshP2PStatus();
        void refreshUserNodeStatus();

        const sessionApi = createDesktopApi(nextApiUrl, transport || undefined);
        let nextPage: Page = "login";
        const authed = await sessionApi.loadCurrentUser()
          .then((response) => {
            if (cancelled) return true;
            startTransition(() => {
              setUser(response.user);
              setError("");
            });
            nextPage = "home";
            void refreshUserServices(sessionApi);
            void refreshAdminData(sessionApi, response.user);
            void syncUserNodePresence({
              email: response.user.email,
              role: response.user.role,
              displayName: response.user.displayName,
            });
            return true;
          })
          .catch(() => false);
        if (cancelled) return;

        const rememberedPassword = await rememberedPasswordPromise;
        if (cancelled) return;

        if (lastProfile) {
          startTransition(() => {
            setLoginPassword(rememberedPassword);
            setSavePassword(Boolean(rememberedPassword));
            setAutoLogin(Boolean(rememberedPassword) && lastProfile.autoLogin);
          });
        }

        if (!authed && lastProfile?.autoLogin && rememberedPassword) {
          try {
            const response = await sessionApi.login(lastEmail, rememberedPassword);
            if (!cancelled) {
              startTransition(() => {
                setUser(response.user);
                setError("");
              });
              nextPage = "home";
              void refreshUserServices(sessionApi);
              void refreshAdminData(sessionApi, response.user);
              void syncUserNodePresence({
                email: response.user.email,
                role: response.user.role,
                displayName: response.user.displayName,
              });
            }
          } catch {
            if (!cancelled) {
              startTransition(() => {
                setUser(null);
              });
            }
          }
        }
        if (!cancelled) {
          startTransition(() => {
            setPage(nextPage);
          });
        }
      } finally {
        if (!cancelled) {
          setInitializing(false);
        }
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [refreshAdminData, refreshP2PStatus, refreshUserNodeStatus, refreshUserServices, syncUserNodePresence, transport]);

  useEffect(() => {
    if (!("__TAURI_INTERNALS__" in window)) return;
    let closeDispose: undefined | (() => void);
    let relaunchDispose: undefined | (() => void);
    void listen("app-close-requested", () => {
      setCloseDialogOpen(true);
    }).then((dispose) => {
      closeDispose = dispose;
    }).catch(() => {
      closeDispose = undefined;
    });
    void listen("second-launch-requested", () => {
      setReopenDialogOpen(true);
    }).then((dispose) => {
      relaunchDispose = dispose;
    }).catch(() => {
      relaunchDispose = undefined;
    });
    return () => {
      closeDispose?.();
      relaunchDispose?.();
    };
  }, []);

  const saveAndReconnect = useCallback(async () => {
    setBusy(true);
    setError("");
    try {
      await persistConfig({ ...appConfig, apiBaseUrl: apiUrl });
      const ok = await loadCurrentSession();
      if (!ok) {
        setPage("login");
      }
      setNotice("配置已保存。");
    } catch (saveError) {
      setError(saveError instanceof Error ? saveError.message : "保存失败");
    } finally {
      setBusy(false);
    }
  }, [apiUrl, appConfig, loadCurrentSession, persistConfig]);

  const handleLogin = useCallback(async () => {
    if (!loginEmail.trim() || !loginPassword.trim()) {
      setError("请输入邮箱和密码");
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await persistConfig({ ...appConfig, apiBaseUrl: apiUrl });
      const loginApi = createDesktopApi(normalizeApiBaseUrl(apiUrl), transport || undefined);
      const response = await loginApi.login(loginEmail.trim(), loginPassword);
      setUser(response.user);
      setPage("home");
      setAuthMode("login");
      await refreshUserServices(loginApi);
      await refreshAdminData(loginApi, response.user);
      void syncUserNodePresence({
        email: response.user.email,
        role: response.user.role,
        displayName: response.user.displayName,
      });
      if (savePassword) {
        await saveLoginProfile(loginEmail.trim(), loginPassword, autoLogin);
        await refreshLoginProfiles();
      }
      setNotice("登录成功。");
    } catch (loginError) {
      setError(loginError instanceof Error ? loginError.message : "登录失败");
    } finally {
      setBusy(false);
    }
  }, [apiUrl, appConfig, autoLogin, loginEmail, loginPassword, persistConfig, refreshAdminData, refreshLoginProfiles, refreshUserServices, savePassword, syncUserNodePresence, transport]);

  const handleRegister = useCallback(async () => {
    if (!registerDisplayName.trim() || !loginEmail.trim() || !loginPassword.trim()) {
      setError("请输入昵称、邮箱和密码");
      return;
    }
    if (loginPassword !== registerConfirmPassword) {
      setError("两次输入的密码不一致");
      return;
    }
    setBusy(true);
    setError("");
    setNotice("");
    try {
      await persistConfig({ ...appConfig, apiBaseUrl: apiUrl });
      const registerApi = createDesktopApi(normalizeApiBaseUrl(apiUrl), transport || undefined);
      const response = await registerApi.register(loginEmail.trim(), registerDisplayName.trim(), loginPassword);
      setUser(response.user);
      setPage("home");
      setAuthMode("login");
      await refreshUserServices(registerApi);
      await refreshAdminData(registerApi, response.user);
      void syncUserNodePresence({
        email: response.user.email,
        role: response.user.role,
        displayName: response.user.displayName,
      });
      if (savePassword) {
        await saveLoginProfile(loginEmail.trim(), loginPassword, autoLogin);
        await refreshLoginProfiles();
      }
      setNotice("注册成功，已自动登录。");
    } catch (registerError) {
      setError(registerError instanceof Error ? registerError.message : "注册失败");
    } finally {
      setBusy(false);
    }
  }, [apiUrl, appConfig, autoLogin, loginEmail, loginPassword, persistConfig, refreshAdminData, refreshLoginProfiles, refreshUserServices, registerConfirmPassword, registerDisplayName, savePassword, syncUserNodePresence, transport]);

  const handleLogout = useCallback(async () => {
    setBusy(true);
    try {
      await api.logout();
      await syncUserNodePresence({ email: "", role: "", displayName: "" }).catch(() => undefined);
    } catch {
      // ignore logout transport errors
    } finally {
      setUser(null);
      setUserServices([]);
      setManagedUsers([]);
      setUserRoleDrafts({});
      setAuthSettings(defaultAuthSettings);
      setPage("login");
      setBusy(false);
    }
  }, [api, syncUserNodePresence]);

  const handleCreateManagedUser = useCallback(async () => {
    if (!user || !isSuperAdmin(user)) {
      setError("只有超级管理员可以创建用户。");
      return;
    }
    if (!managedUserForm.email.trim() || !managedUserForm.displayName.trim() || !managedUserForm.password.trim()) {
      setError("请输入新用户的邮箱、昵称和密码");
      return;
    }
    setAdminBusyAction("create-user");
    setError("");
    setNotice("");
    try {
      await api.createManagedUser(managedUserForm);
      setManagedUserForm({ email: "", displayName: "", password: "", role: "user" });
      await refreshAdminData();
      setNotice("用户已创建。");
    } catch (adminError) {
      setError(adminError instanceof Error ? adminError.message : "创建用户失败");
    } finally {
      setAdminBusyAction("");
    }
  }, [api, managedUserForm, refreshAdminData, user]);

  const handleTogglePublicRegistration = useCallback(async (enabled: boolean) => {
    if (!user || !isSuperAdmin(user)) {
      setError("只有超级管理员可以修改公开注册开关。");
      return;
    }
    setAdminBusyAction("toggle-registration");
    setError("");
    setNotice("");
    try {
      const nextSettings = await api.updateAuthSettings(enabled);
      setAuthSettings(nextSettings);
      setNotice(enabled ? "已开启公开注册。" : "已关闭公开注册。");
    } catch (adminError) {
      setError(adminError instanceof Error ? adminError.message : "更新注册开关失败");
    } finally {
      setAdminBusyAction("");
    }
  }, [api, user]);

  const handleSaveManagedUserRole = useCallback(async (target: UserSummary) => {
    if (!canEditUserRole(user, target)) {
      setError("只有超级管理员可以调整用户权限。");
      return;
    }
    const nextRole = userRoleDrafts[target.id] || target.role;
    if (nextRole === target.role) {
      setNotice("角色未发生变化。");
      return;
    }
    setAdminBusyAction("role:" + target.id);
    setError("");
    setNotice("");
    try {
      await api.updateManagedUser(target.id, { role: nextRole });
      await refreshAdminData();
      setNotice("用户角色已更新。");
    } catch (adminError) {
      setError(adminError instanceof Error ? adminError.message : "更新用户角色失败");
    } finally {
      setAdminBusyAction("");
    }
  }, [api, refreshAdminData, user, userRoleDrafts]);

  const handleToggleManagedUserDisabled = useCallback(async (target: UserSummary) => {
    if (!canToggleUserDisabled(user, target)) {
      setError(isOrdinaryAdmin(user) ? "普通管理员只能封禁或解禁普通用户。" : "当前不能调整该账号状态。");
      return;
    }
    setAdminBusyAction("toggle-user:" + target.id);
    setError("");
    setNotice("");
    try {
      await api.updateManagedUser(target.id, { disabled: !Boolean(target.disabled) });
      await refreshAdminData();
      setNotice(target.disabled ? "用户已解禁。" : "用户已封禁。");
    } catch (adminError) {
      setError(adminError instanceof Error ? adminError.message : "更新用户状态失败");
    } finally {
      setAdminBusyAction("");
    }
  }, [api, refreshAdminData, user]);

  const handleSendPasswordChangeCode = useCallback(async () => {
    if (isSuperAdmin(user)) {
      setError("超级管理员密码不提供自助修改，请走平台外部人工修改流程。");
      return;
    }
    setAdminBusyAction("send-password-code");
    setError("");
    setNotice("");
    try {
      const response = await api.sendPasswordChangeCode();
      setNotice(`验证码已发送到 ${response.email}。`);
    } catch (adminError) {
      setError(adminError instanceof Error ? adminError.message : "发送验证码失败");
    } finally {
      setAdminBusyAction("");
    }
  }, [api, user]);

  const handleConfirmPasswordChange = useCallback(async () => {
    if (isSuperAdmin(user)) {
      setError("超级管理员密码不提供自助修改，请走平台外部人工修改流程。");
      return;
    }
    if (!passwordChangeForm.code.trim() || !passwordChangeForm.password.trim()) {
      setError("请输入验证码和新密码");
      return;
    }
    if (passwordChangeForm.password !== passwordChangeForm.confirmPassword) {
      setError("两次输入的新密码不一致");
      return;
    }
    setAdminBusyAction("confirm-password-change");
    setError("");
    setNotice("");
    try {
      const response = await api.confirmPasswordChange(passwordChangeForm.code.trim(), passwordChangeForm.password);
      setUser(response.user);
      setPasswordChangeForm({ code: "", password: "", confirmPassword: "" });
      setNotice("当前账号密码已更新。");
    } catch (adminError) {
      setError(adminError instanceof Error ? adminError.message : "修改密码失败");
    } finally {
      setAdminBusyAction("");
    }
  }, [api, passwordChangeForm, user]);

  const handleDeleteProfile = useCallback(async (email: string) => {
    try {
      await deleteLoginProfile(email);
      const updated = await refreshLoginProfiles();
      if (loginEmail === email) {
        const nextEmail = updated?.lastUsedEmail || updated?.profiles[0]?.email || "";
        if (nextEmail) {
          await fillProfileCredentials(nextEmail, updated);
        } else {
          setLoginEmail("");
          setLoginPassword("");
          setSavePassword(false);
          setAutoLogin(false);
        }
      }
    } catch (deleteError) {
      setError(deleteError instanceof Error ? deleteError.message : "删除失败");
    }
  }, [fillProfileCredentials, loginEmail, refreshLoginProfiles]);

  const handleSilentStartToggle = useCallback(async (enabled: boolean) => {
    const previous = Boolean(appConfig.silentStart);
    const nextConfig = { ...appConfig, silentStart: enabled };
    setAppConfig(nextConfig);
    setError("");
    try {
      await saveAppConfig(nextConfig);
    } catch (saveError) {
      setAppConfig((current) => ({ ...mergeConfig(current), silentStart: previous }));
      setError(saveError instanceof Error ? saveError.message : "保存静默启动设置失败");
    }
  }, [appConfig]);

  const handleAutoStartToggle = useCallback(async (enabled: boolean) => {
    const previous = Boolean(appConfig.autoStart);
    const nextConfig = { ...appConfig, autoStart: enabled };
    setAppConfig(nextConfig);
    setError("");
    try {
      await setAutoStart(enabled);
      const actual = transport ? await loadAutoStartEnabled() : enabled;
      if (actual !== enabled) {
        throw new Error(enabled ? "开机自启注册校验失败" : "开机自启取消校验失败");
      }
      const verifiedConfig = { ...nextConfig, autoStart: actual };
      setAppConfig(verifiedConfig);
      await saveAppConfig(verifiedConfig);
    } catch (autoStartError) {
      const actual = transport ? await loadAutoStartEnabled().catch(() => previous) : previous;
      setAppConfig((current) => ({ ...mergeConfig(current), autoStart: actual }));
      setError(autoStartError instanceof Error ? autoStartError.message : "设置开机自启失败");
    }
  }, [appConfig, transport]);

  const handleWindowClose = useCallback(() => {
    const closeAction = (appConfig.closeAction || defaultConfig.closeAction) as CloseAction;
    if (closeAction === "ask") {
      setCloseDialogOpen(true);
      return;
    }
    if (closeAction === "tray") {
      void windowRequestClose();
      return;
    }
    void appExit();
  }, [appConfig.closeAction]);

  const handleCloseDialogChoice = useCallback(async (action: "tray" | "exit", dontAskAgain: boolean) => {
    setCloseDialogOpen(false);
    if (dontAskAgain) {
      const nextConfig = { ...appConfig, closeAction: action };
      setAppConfig(nextConfig);
      try {
        await saveAppConfig(nextConfig);
      } catch {
        // ignore
      }
    }
    if (action === "tray") {
      void windowRequestClose();
      return;
    }
    void appExit();
  }, [appConfig]);

  const handleReopenChoice = useCallback((_choice: ReopenDialogChoice) => {
    setReopenDialogOpen(false);
  }, []);

  const handleTitlebarDrag = useCallback((event: MouseEvent<HTMLDivElement>) => {
    if (event.button !== 0) return;
    const target = event.target as HTMLElement | null;
    if (target?.closest("button, input, select, textarea, a, label")) return;
    void windowStartDrag();
  }, []);

  useEffect(() => {
    if (initializing || page !== "p2p") return;
    void refreshP2PStatus();
    void refreshUserNodeStatus();
    const timer = window.setInterval(() => {
      void refreshP2PStatus();
      void refreshUserNodeStatus();
    }, 5000);
    return () => {
      window.clearInterval(timer);
    };
  }, [initializing, page, refreshP2PStatus, refreshUserNodeStatus]);

  const saveP2PSettings = useCallback(async () => {
    setP2PBusy(true);
    setError("");
    try {
      const merged = mergeConfig(appConfig);
      setAppConfig(merged);
      await saveAppConfig(merged);
      await refreshP2PStatus();
      void syncUserNodePresence(user ? {
        email: user.email,
        role: user.role,
        displayName: user.displayName,
      } : undefined);
      setNotice("P2P 设置已保存。当前不会自动重启；若 EasyTier 已在运行，请手动停止后再启动以应用新参数。");
    } catch (configError) {
      setError(configError instanceof Error ? configError.message : "保存 P2P 设置失败");
    } finally {
      setP2PBusy(false);
    }
  }, [appConfig, refreshP2PStatus, syncUserNodePresence, user]);

  const handleP2PRuntimeStart = useCallback(async () => {
    if (p2pStatus?.running) {
      setNotice("EasyTier 已在运行，本次不会重复拉起。");
      return;
    }
    setP2PBusy(true);
    setError("");
    setNotice("");
    try {
      const merged = mergeConfig(appConfig);
      setAppConfig(merged);
      await saveAppConfig(merged);
      const status = await startP2PRuntime();
      setP2PStatus(status);
      void syncUserNodePresence(user ? {
        email: user.email,
        role: user.role,
        displayName: user.displayName,
      } : undefined);
      setNotice(status.peerCount > 0 ? `EasyTier 已启动并接入 ${status.peerCount} 个对等节点。` : "EasyTier 已启动。");
    } catch (startError) {
      setError(startError instanceof Error ? startError.message : "启动 EasyTier 失败");
    } finally {
      setP2PBusy(false);
    }
  }, [appConfig, p2pStatus?.running, syncUserNodePresence, user]);

  const handleP2PRuntimeStop = useCallback(async () => {
    setP2PBusy(true);
    setError("");
    setNotice("");
    try {
      const status = await stopP2PRuntime();
      setP2PStatus(status);
      void syncUserNodePresence(user ? {
        email: user.email,
        role: user.role,
        displayName: user.displayName,
      } : undefined);
      setNotice("EasyTier 已停止。");
    } catch (stopError) {
      setError(stopError instanceof Error ? stopError.message : "停止 EasyTier 失败");
    } finally {
      setP2PBusy(false);
    }
  }, [syncUserNodePresence, user]);

  const handleOpenP2PLog = useCallback(async (kind: "stdout" | "stderr") => {
    try {
      await openP2PRuntimeLog(kind);
    } catch (logError) {
      setError(logError instanceof Error ? logError.message : "打开日志失败");
    }
  }, []);

  const handleLaunchServiceWorkspace = useCallback(async (service: ServiceBinding | null) => {
    if (!service?.p2pUrl) {
      setError("当前服务还没有登记 P2P 入口。");
      return;
    }
    if (!service.p2pAllowed) {
      setError("当前账号没有这个 P2P 服务入口权限。");
      return;
    }
    if (!p2pStatus?.running) {
      setError("请先启动 EasyTier，再打开 P2P 服务工作台。");
      return;
    }
    try {
      await probeServiceWorkspace(service.p2pUrl);
      await openServiceWorkspace(service.key, `驻阡陌用户端 - ${service.title}`, service.p2pUrl);
    } catch (openError) {
      const publicHost = hostFromUrl(service.publicUrl);
      if (publicHost) {
        try {
          await probeServiceWorkspace(service.p2pUrl, publicHost);
          setError(`当前 ${service.title} 的 P2P 地址已经可达，但服务本身依赖域名 Host 返回页面。用户端还需要补本地代理改写，直接开窗会变成空白页。`);
          return;
        } catch {
          // keep original error below
        }
      }
      setError(openError instanceof Error ? `启动服务工作台失败：${openError.message}` : "启动服务工作台失败");
    }
  }, [p2pStatus?.running]);

  const handleOpenServiceExternal = useCallback(async (service: ServiceBinding | null) => {
    if (!service?.p2pUrl) {
      setError("当前服务还没有登记 P2P 入口。");
      return;
    }
    if (!service.p2pAllowed) {
      setError("当前账号没有这个 P2P 服务入口权限。");
      return;
    }
    if (!p2pStatus?.running) {
      setError("请先启动 EasyTier，再打开 P2P 服务入口。");
      return;
    }
    try {
      await probeServiceWorkspace(service.p2pUrl);
      await openServiceWorkspaceExternal(service.key, `驻阡陌用户端 - ${service.title}`, service.p2pUrl);
    } catch (openError) {
      setError(openError instanceof Error ? `打开浏览器失败：${openError.message}` : "打开浏览器失败");
    }
  }, [p2pStatus?.running]);

  const handleCopyValue = useCallback(async (value: string, label: string) => {
    try {
      if (!navigator?.clipboard?.writeText) {
        throw new Error("当前环境不支持剪贴板");
      }
      await navigator.clipboard.writeText(value);
      setNotice(`${label}已复制。`);
    } catch (copyError) {
      setError(copyError instanceof Error ? copyError.message : `复制${label}失败`);
    }
  }, []);

  const renderLoginPage = () => (
    <div className="card auth-card">
      <h2>{authMode === "register" ? "注册普通用户" : "登录用户端"}</h2>
      <p className="helper-text">
        用户端用于访问网盘、图床和 P2P 网络。图床、网盘自身仍会做业务登录，所以用户端不再额外限制这两个服务的 P2P 入口。
      </p>
      <label>云端地址</label>
      <input value={apiUrl} onChange={(event) => setApiUrl(event.target.value)} placeholder={defaultApiUrl} />
      <label>邮箱</label>
      {authMode === "login" && loginProfiles?.profiles.length ? (
        <div className="profile-selector">
          <div className="profile-input-wrap">
            <input value={loginEmail} onChange={(event) => setLoginEmail(event.target.value)} onFocus={() => setProfileListOpen(false)} />
            <button type="button" className="profile-arrow-btn" onClick={() => setProfileListOpen((current) => !current)}>
              {profileListOpen ? "▴" : "▾"}
            </button>
          </div>
          {profileListOpen ? (
            <div className="profile-dropdown">
              {loginProfiles.profiles.map((profile) => (
                <div
                  key={profile.email}
                  className={`profile-row${loginEmail === profile.email ? " selected" : ""}`}
                  onClick={() => void fillProfileCredentials(profile.email)}
                >
                  <span className="profile-email">{profile.email}{profile.autoLogin ? " (自动登录)" : ""}</span>
                  <button
                    type="button"
                    className="profile-delete-btn"
                    onClick={(event) => {
                      event.stopPropagation();
                      setDeleteConfirmTarget(profile.email);
                    }}
                  >
                    删除
                  </button>
                </div>
              ))}
            </div>
          ) : null}
        </div>
      ) : (
        <input value={loginEmail} onChange={(event) => setLoginEmail(event.target.value)} />
      )}
      {authMode === "register" ? (
        <>
          <label>昵称</label>
          <input value={registerDisplayName} onChange={(event) => setRegisterDisplayName(event.target.value)} />
        </>
      ) : null}
      <label>密码</label>
      <input type="password" value={loginPassword} onChange={(event) => setLoginPassword(event.target.value)} />
      {authMode === "register" ? (
        <>
          <label>确认密码</label>
          <input type="password" value={registerConfirmPassword} onChange={(event) => setRegisterConfirmPassword(event.target.value)} />
        </>
      ) : null}
      <label className="check-row">
        <input
          type="checkbox"
          checked={savePassword}
          onChange={(event) => {
            if (autoLogin && !event.target.checked) return;
            setSavePassword(event.target.checked);
          }}
          disabled={autoLogin}
        />
        <span>保存密码{autoLogin ? "（自动登录需要）" : ""}</span>
      </label>
      <label className="check-row">
        <input
          type="checkbox"
          checked={autoLogin}
          onChange={(event) => {
            const next = event.target.checked;
            setAutoLogin(next);
            if (next) {
              setSavePassword(true);
            }
          }}
        />
        <span>自动登录</span>
      </label>
      <div className="action-row">
        <button
          className="primary"
          type="button"
          onClick={() => void (authMode === "register" ? handleRegister() : handleLogin())}
          disabled={busy}
        >
          {busy ? (authMode === "register" ? "注册中..." : "登录中...") : (authMode === "register" ? "注册并登录" : "登录")}
        </button>
        <button className="secondary" type="button" onClick={() => void saveAndReconnect()} disabled={busy}>
          保存地址
        </button>
      </div>
      <div className="action-row">
        <button
          className="secondary"
          type="button"
          onClick={() => {
            setAuthMode((current) => current === "login" ? "register" : "login");
            setError("");
            setNotice("");
            setProfileListOpen(false);
          }}
          disabled={busy}
        >
          {authMode === "register" ? "返回登录" : "注册普通用户"}
        </button>
      </div>
    </div>
  );

  const userConnectorModeLabel = "用户端内仅启动 P2P 工作台";
  const galleryPublicModeLabel = "公网 HTTPS 入口继续保留在云端，但不在用户端内启用";
  const catalogByKey = useMemo(() => {
    const map = new Map<string, UserServiceEntry[]>();
    for (const item of userServices) {
      const existing = map.get(item.key) || [];
      existing.push(item);
      map.set(item.key, existing);
    }
    return map;
  }, [userServices]);
  const driveService = useMemo(() => resolveServiceBinding(
    "drive",
    "网盘服务",
    "面向大文件下载，云端只保留轻量入口与目录能力，数据面优先走 P2P。",
    catalogByKey.get("drive"),
    appConfig.driveCloudUrl,
    appConfig.driveP2pUrl,
  ), [appConfig.driveCloudUrl, appConfig.driveP2pUrl, catalogByKey]);
  const galleryService = useMemo(() => resolveServiceBinding(
    "gallery",
    "图床服务",
    "轻量读取和日常上传走云端，批量上传优先走 P2P。",
    catalogByKey.get("gallery"),
    appConfig.galleryCloudUrl,
    appConfig.galleryP2pUrl,
  ), [appConfig.galleryCloudUrl, appConfig.galleryP2pUrl, catalogByKey]);
  const extraServiceBindings = useMemo(() => {
    const items: ServiceBinding[] = [];
    for (const [key, group] of catalogByKey.entries()) {
      if (key === "drive" || key === "gallery") {
        continue;
      }
      const binding = resolveServiceBinding(
        key,
        firstCatalogEntry(group)?.title || key,
        firstCatalogEntry(group)?.summary || "已在云端目录中登记，可从用户端直接打开。",
        group,
      );
      if (binding) {
        items.push(binding);
      }
    }
    return items;
  }, [catalogByKey]);

  const managementVisible = Boolean(user && (isSuperAdmin(user) || isOrdinaryAdmin(user)));
  const superManagementVisible = Boolean(user && isSuperAdmin(user));

  return (
    <div className="app-shell">
      <div className="titlebar" onMouseDown={handleTitlebarDrag}>
        <span className="titlebar-text">驻阡陌用户端</span>
        <div className="titlebar-actions">
          <button className="tb-btn" type="button" onClick={() => void windowMinimize()}>─</button>
          <button className="tb-btn" type="button" onClick={() => void windowToggleMaximize()}>□</button>
          <button className="tb-btn close" type="button" onClick={handleWindowClose}>×</button>
        </div>
      </div>

      <div className="sidebar">
        <div className="sidebar-brand">驻阡陌用户端</div>
        {user ? (
          <>
            <button className={`nav-btn ${page === "home" ? "active" : ""}`} type="button" onClick={() => setPage("home")}>连接器</button>
            <button className={`nav-btn ${page === "drive" ? "active" : ""}`} type="button" onClick={() => setPage("drive")}>网盘</button>
            <button className={`nav-btn ${page === "gallery" ? "active" : ""}`} type="button" onClick={() => setPage("gallery")}>图床</button>
            <button className={`nav-btn ${page === "p2p" ? "active" : ""}`} type="button" onClick={() => setPage("p2p")}>P2P 网络</button>
            {(isSuperAdmin(user) || isOrdinaryAdmin(user)) ? (
              <button className={`nav-btn ${page === "management" ? "active" : ""}`} type="button" onClick={() => setPage("management")}>
                {isSuperAdmin(user) ? "超级管理" : "管理页"}
              </button>
            ) : null}
            <button className={`nav-btn ${page === "settings" ? "active" : ""}`} type="button" onClick={() => setPage("settings")}>设置</button>
            <div className="sidebar-footer">
              <div className="user-info">
                <div>{user.email}</div>
                <div className="user-role">{roleLabel(user.role)}</div>
              </div>
              <button className="logout-btn" type="button" onClick={() => void handleLogout()}>退出登录</button>
            </div>
          </>
        ) : (
          <div className="sidebar-footer">
            <div className="user-info">请先登录云端</div>
          </div>
        )}
      </div>

      <main className="main">
        {error ? <div className="error-bar" onClick={() => setError("")}>{error}</div> : null}
        {notice ? <div className="success-bar" onClick={() => setNotice("")}>{notice}</div> : null}

        {initializing || page === "loading" ? (
          <div className="card">
            <h2>初始化中</h2>
            <p className="helper-text">正在读取本地配置、登录状态和用户端工作目录。</p>
          </div>
        ) : null}

        {!initializing && page === "login" ? renderLoginPage() : null}

        {!initializing && user && page === "home" ? (
          <>
            <div className="page-header">
              <h1>P2P 连接器</h1>
              <p>云端负责控制面与公网入口，服务端承载真实业务，用户端作为 P2P 连接器来启动并使用这些服务。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>角色边界</h2>
                <div className="info-list">
                  <div><strong>云端</strong><span>负责登录、授权、公开域名、轻量入口与回退策略。</span></div>
                  <div><strong>服务端</strong><span>部署网盘、图床等真实业务服务，并加入 EasyTier。</span></div>
                  <div><strong>用户端</strong><span>安装独立壳，加入 EasyTier，负责 P2P 下载和批量上传。</span></div>
                </div>
              </section>
              <section className="card">
                <h2>当前策略</h2>
                <div className="policy-chip">用户端模式: {userConnectorModeLabel}</div>
                <div className="policy-chip">网盘工作台: 仅 P2P 启动</div>
                <div className="policy-chip">图床工作台: 仅 P2P 启动</div>
                <div className="policy-chip">图床公网读取: {galleryPublicModeLabel}</div>
                <div className="policy-chip">P2P 随应用启动: {appConfig.p2pAutoStart ? "已开启" : "未开启"}</div>
                <div className="policy-chip">EasyTier 运行态: {p2pRuntimeLabel(p2pStatus)}</div>
                <div className="policy-chip">本机节点: {p2pStatus?.nodeHostname || "待上报"}</div>
                <div className="policy-chip">虚拟 IPv4: {p2pStatus?.virtualIpv4 || "未分配"}</div>
                <div className="policy-chip">当前对等节点: {p2pStatus?.peerCount ?? 0}</div>
                <div className="policy-chip">后台节点注册: {userNodeStatusLabel(userNodeStatus)}</div>
                <div className="policy-chip">后台节点 ID: {userNodeStatus?.nodeId || "待生成"}</div>
              </section>
            </div>
            <section className="card" style={{ marginTop: 18 }}>
              <div className="service-header-row">
                <div>
                  <h2>服务工作台</h2>
                  <p className="helper-text">这里展示的是用户端可启动的 P2P 服务工作台。云端公网入口继续存在，但不在这里作为数据面入口。</p>
                </div>
                <button className="secondary" type="button" onClick={() => void refreshUserServices()} disabled={serviceLoading}>
                  {serviceLoading ? "刷新中..." : "刷新服务目录"}
                </button>
              </div>
              <div className="service-grid">
                <div className="service-tile">
                  <div className="service-tile-top">
                    <strong>网盘</strong>
                    <span className="service-badge">{driveService ? serviceSourceLabel(driveService.source) : "未配置"}</span>
                  </div>
                  <p>{driveService?.summary || "尚未在云端目录或本地设置中登记网盘入口。"}</p>
                  <div className="service-chip-row">
                    <span className="service-chip">{driveService?.p2pUrl ? "P2P 工作台可启动" : "P2P 待配置"}</span>
                    <span className="service-chip">{driveService?.nodeName ? driveService.nodeName : "未绑定服务端"}</span>
                  </div>
                  <div className="action-row wrap">
                    <button className="primary" type="button" onClick={() => void handleLaunchServiceWorkspace(driveService)} disabled={!driveService?.p2pUrl || !driveService?.p2pAllowed || !p2pStatus?.running}>
                      启动网盘工作台
                    </button>
                    <button className="secondary" type="button" onClick={() => void handleOpenServiceExternal(driveService)} disabled={!driveService?.p2pUrl || !driveService?.p2pAllowed || !p2pStatus?.running}>
                      浏览器打开
                    </button>
                  </div>
                </div>
                <div className="service-tile">
                  <div className="service-tile-top">
                    <strong>图床</strong>
                    <span className="service-badge">{galleryService ? serviceSourceLabel(galleryService.source) : "未配置"}</span>
                  </div>
                  <p>{galleryService?.summary || "尚未在云端目录或本地设置中登记图床入口。"}</p>
                  <div className="service-chip-row">
                    <span className="service-chip">{galleryService?.p2pUrl ? "P2P 工作台可启动" : "P2P 待配置"}</span>
                    <span className="service-chip">{galleryService?.publicUrl ? "公网 HTTPS 仍保留" : "公网入口未登记"}</span>
                  </div>
                  <div className="action-row wrap">
                    <button className="primary" type="button" onClick={() => void handleLaunchServiceWorkspace(galleryService)} disabled={!galleryService?.p2pUrl || !galleryService?.p2pAllowed || !p2pStatus?.running}>
                      启动图床工作台
                    </button>
                    <button className="secondary" type="button" onClick={() => void handleOpenServiceExternal(galleryService)} disabled={!galleryService?.p2pUrl || !galleryService?.p2pAllowed || !p2pStatus?.running}>
                      浏览器打开
                    </button>
                  </div>
                </div>
                {extraServiceBindings.map((service) => (
                  <div key={service.key} className="service-tile">
                    <div className="service-tile-top">
                      <strong>{service.title}</strong>
                      <span className="service-badge">{service.kind}</span>
                    </div>
                    <p>{service.summary || "已登记为额外服务入口。"}</p>
                    <div className="service-chip-row">
                      <span className="service-chip">{service.p2pUrl ? "P2P 工作台可启动" : "无 P2P 入口"}</span>
                      <span className="service-chip">{service.publicUrl ? "公网入口另保留" : "无公网入口"}</span>
                    </div>
                    <div className="action-row wrap">
                      <button className="primary" type="button" onClick={() => void handleLaunchServiceWorkspace(service)} disabled={!service.p2pUrl || !service.p2pAllowed || !p2pStatus?.running}>
                        启动工作台
                      </button>
                      <button className="secondary" type="button" onClick={() => void handleOpenServiceExternal(service)} disabled={!service.p2pUrl || !service.p2pAllowed || !p2pStatus?.running}>
                        浏览器打开
                      </button>
                    </div>
                  </div>
                ))}
              </div>
            </section>
          </>
        ) : null}

        {!initializing && user && page === "drive" ? (
          <>
            <div className="page-header">
              <h1>网盘访问</h1>
              <p>这里不是云端回退页，而是用户端网盘 P2P 工作台启动页。真正的数据面只走 P2P。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <div className="service-header-row">
                  <div>
                    <h2>服务接入</h2>
                    <p className="helper-text">网盘页优先接入服务端 P2P 下载入口；云端入口只用于目录、鉴权和受控回退。</p>
                  </div>
                  <button className="secondary" type="button" onClick={() => void refreshUserServices()} disabled={serviceLoading}>
                    {serviceLoading ? "刷新中..." : "刷新目录"}
                  </button>
                </div>
                {driveService ? (
                  <>
                    <div className="policy-chip">来源: {serviceSourceLabel(driveService.source)}</div>
                    <div className="policy-chip">用户端模式: 仅 P2P 工作台</div>
                    <div className="policy-chip">P2P 权限: {serviceAccessLabel(driveService.p2pAccess)}</div>
                    <label>云端目录 / 公网入口（仅展示）</label>
                    <div className="mono-box">{driveService.publicUrl || "未配置"}</div>
                    <label>P2P 入口</label>
                    <div className="mono-box">{driveService.p2pUrl || "未配置"}</div>
                    <div className="action-row wrap">
                      <button
                        className="primary"
                        type="button"
                        onClick={() => void handleLaunchServiceWorkspace(driveService)}
                        disabled={!driveService.p2pUrl || !driveService.p2pAllowed || !p2pStatus?.running}
                      >
                        启动网盘工作台
                      </button>
                      <button
                        className="secondary"
                        type="button"
                        onClick={() => void handleOpenServiceExternal(driveService)}
                        disabled={!driveService.p2pUrl || !driveService.p2pAllowed || !p2pStatus?.running}
                      >
                        浏览器打开
                      </button>
                      <button
                        className="secondary"
                        type="button"
                        onClick={() => void handleCopyValue(driveService.p2pUrl, "网盘 P2P 地址")}
                        disabled={!driveService.p2pUrl}
                      >
                        复制 P2P 地址
                      </button>
                    </div>
                  </>
                ) : (
                  <div className="service-empty">
                    <strong>当前还没有可用的网盘服务入口</strong>
                    <p>你可以先在设置页手填 `driveCloudUrl` / `driveP2pUrl`，或者在云端 tunnel 元数据里登记 `serviceKey=drive`。</p>
                  </div>
                )}
              </section>
              <section className="card">
                <h2>接入状态</h2>
                <div className="state-row"><span>用户角色</span><span>{roleLabel(user.role)}</span></div>
                <div className="state-row"><span>用户端模式</span><span>{userConnectorModeLabel}</span></div>
                <div className="state-row"><span>P2P 客户端</span><span>{p2pRuntimeLabel(p2pStatus)}</span></div>
                <div className="state-row"><span>虚拟 IPv4</span><span>{p2pStatus?.virtualIpv4 || "未分配"}</span></div>
                <div className="state-row"><span>服务节点</span><span>{driveService?.nodeName || "未登记"}</span></div>
                <div className="state-row"><span>节点状态</span><span>{nodeStatusLabel(driveService?.nodeStatus)}</span></div>
                <div className="state-row"><span>隧道健康</span><span>{healthStatusLabel(driveService?.healthStatus)}</span></div>
                <div className="state-row"><span>运行态</span><span>{driveService?.runtimePath || "未上报"} / {driveService?.runtimeState || "未上报"}</span></div>
              </section>
            </div>
            <section className="card" style={{ marginTop: 18 }}>
              <h2>当前策略说明</h2>
              <ul className="plain-list">
                <li>用户端里的网盘工作台只通过 P2P 地址启动，不在这里启用云端数据面下载。</li>
                <li>云端入口继续承担目录、分享和公开访问，但与用户端大流量下载逻辑分离。</li>
                <li>这样才能把服务器流量和用户端 P2P 流量清晰分开，后续再独立做 P2P 统计页。</li>
              </ul>
            </section>
          </>
        ) : null}

        {!initializing && user && page === "gallery" ? (
          <>
            <div className="page-header">
              <h1>图床</h1>
              <p>用户端里的图床工作台只启动 P2P 入口。公网 HTTPS 仍然保留给外链展示和云端访问，但不在这里作为数据面。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <div className="service-header-row">
                  <div>
                    <h2>入口接入</h2>
                    <p className="helper-text">图床保留公开 HTTPS 读图入口，同时为批量上传单独登记 P2P 入口。</p>
                  </div>
                  <button className="secondary" type="button" onClick={() => void refreshUserServices()} disabled={serviceLoading}>
                    {serviceLoading ? "刷新中..." : "刷新目录"}
                  </button>
                </div>
                {galleryService ? (
                  <>
                    <div className="policy-chip">来源: {serviceSourceLabel(galleryService.source)}</div>
                    <div className="policy-chip">用户端模式: 仅 P2P 工作台</div>
                    <label>公网 / 轻量 HTTPS 入口（仅展示）</label>
                    <div className="mono-box">{galleryService.publicUrl || "未配置"}</div>
                    <label>批量上传 P2P 入口</label>
                    <div className="mono-box">{galleryService.p2pUrl || "未配置"}</div>
                    <div className="action-row wrap">
                      <button
                        className="primary"
                        type="button"
                        onClick={() => void handleLaunchServiceWorkspace(galleryService)}
                        disabled={!galleryService.p2pUrl || !galleryService.p2pAllowed || !p2pStatus?.running}
                      >
                        启动图床工作台
                      </button>
                      <button
                        className="secondary"
                        type="button"
                        onClick={() => void handleOpenServiceExternal(galleryService)}
                        disabled={!galleryService.p2pUrl || !galleryService.p2pAllowed || !p2pStatus?.running}
                      >
                        浏览器打开
                      </button>
                      <button
                        className="secondary"
                        type="button"
                        onClick={() => void handleCopyValue(galleryService.p2pUrl, "图床 P2P 地址")}
                        disabled={!galleryService.p2pUrl}
                      >
                        复制 P2P 地址
                      </button>
                    </div>
                  </>
                ) : (
                  <div className="service-empty">
                    <strong>当前还没有可用的图床服务入口</strong>
                    <p>你可以先在设置页手填 `galleryCloudUrl` / `galleryP2pUrl`，或者在云端 tunnel 元数据里登记 `serviceKey=gallery`。</p>
                  </div>
                )}
              </section>
              <section className="card">
                <h2>状态与链路</h2>
                <div className="state-row"><span>P2P 客户端</span><span>{p2pRuntimeLabel(p2pStatus)}</span></div>
                <div className="state-row"><span>用户端模式</span><span>{userConnectorModeLabel}</span></div>
                <div className="state-row"><span>服务节点</span><span>{galleryService?.nodeName || "未登记"}</span></div>
                <div className="state-row"><span>节点状态</span><span>{nodeStatusLabel(galleryService?.nodeStatus)}</span></div>
                <div className="state-row"><span>隧道健康</span><span>{healthStatusLabel(galleryService?.healthStatus)}</span></div>
                <div className="state-row"><span>运行态</span><span>{galleryService?.runtimePath || "未上报"} / {galleryService?.runtimeState || "未上报"}</span></div>
                <div className="state-row"><span>对等节点数</span><span>{p2pStatus?.peerCount ?? 0}</span></div>
              </section>
            </div>
            <section className="card" style={{ marginTop: 18 }}>
              <h2>当前策略说明</h2>
              <ul className="plain-list">
                <li>用户端里的图床工作台只通过 P2P 地址启动，上传和下载都不在这里走云端数据面。</li>
                <li>博客图片外链、访客读取、云端轻量访问仍保留 HTTPS，不和用户端流量混算。</li>
                <li>后续新增更多服务时，也沿用“连接器启动 P2P 工作台”的同一套模型。</li>
              </ul>
            </section>
          </>
        ) : null}

        {!initializing && user && page === "p2p" ? (
          <>
            <div className="page-header">
              <h1>P2P 网络</h1>
              <p>P2P 是节点级覆盖网络，不是单条隧道。这里单独管理 EasyTier 生命周期和用户端策略。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>本地策略</h2>
                <label className="check-row">
                  <input
                    type="checkbox"
                    checked={Boolean(appConfig.p2pAutoStart)}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pAutoStart: event.target.checked }))}
                  />
                  <span>随用户端启动 EasyTier</span>
                </label>
                <div className="policy-chip">用户端服务模式: {userConnectorModeLabel}</div>
                <div className="policy-chip">网盘工作台: 只启动 P2P 服务</div>
                <div className="policy-chip">图床工作台: 只启动 P2P 服务</div>
                <div className="policy-chip">公网 HTTPS: 保留在云端，不计入用户端数据面</div>
                <div className="action-row wrap">
                  <button className="primary" type="button" onClick={() => void saveP2PSettings()} disabled={p2pBusy}>
                    {p2pBusy ? "保存中..." : "保存 P2P 设置"}
                  </button>
                </div>
              </section>
              <section className="card">
                <h2>EasyTier 运行态</h2>
                <div className="state-row"><span>当前状态</span><span>{p2pRuntimeLabel(p2pStatus)}</span></div>
                <div className="state-row"><span>进程 PID</span><span>{p2pStatus?.pid ?? "未运行"}</span></div>
                <div className="state-row"><span>最近启动</span><span>{formatStartedAt(p2pStatus?.startedAt)}</span></div>
                <div className="state-row"><span>参数状态</span><span>{p2pStatus?.configured ? "已配置" : "未配置"}</span></div>
                <div className="state-row"><span>本机主机名</span><span>{p2pStatus?.nodeHostname || "待上报"}</span></div>
                <div className="state-row"><span>虚拟 IPv4</span><span>{p2pStatus?.virtualIpv4 || "未分配"}</span></div>
                <div className="state-row"><span>对等节点数</span><span>{p2pStatus?.peerCount ?? 0}</span></div>
                <div className="state-row"><span>后台节点</span><span>{userNodeStatusLabel(userNodeStatus)}</span></div>
                <div className="state-row"><span>后台节点 ID</span><span>{userNodeStatus?.nodeId || "待注册"}</span></div>
                <div className="state-row"><span>归属账号</span><span>{userNodeStatus?.ownerEmail || user?.email || "未登记"}</span></div>
                <div className="state-row"><span>最近注册</span><span>{formatObservedAt(userNodeStatus?.lastRegisterAt)}</span></div>
                <div className="state-row"><span>最近心跳</span><span>{formatObservedAt(userNodeStatus?.lastHeartbeatAt)}</span></div>
                <div className="state-row"><span>machine-id</span><span>{p2pStatus?.machineId || "未生成"}</span></div>
                <div className="state-row"><span>RPC 端口</span><span>{p2pStatus?.rpcPortal || "未配置"}</span></div>
                <label>执行文件</label>
                <div className="mono-box">{p2pStatus?.executablePath || "当前安装包尚未带入 easytier-core.exe"}</div>
                <label>运行目录</label>
                <div className="mono-box">{p2pStatus?.workDir || desktopHostPaths.configDir}</div>
                <label>启动参数摘要</label>
                <div className="mono-box">{p2pStatus?.argsSummary || "当前尚未生成启动参数。先填写下方 EasyTier 节点参数。"}</div>
                <label>当前对等节点</label>
                <div className="mono-box">{p2pStatus?.connectedPeers?.length ? p2pStatus.connectedPeers.join("\n") : "当前尚未接入任何对等节点。"}</div>
                {p2pStatus?.lastError ? (
                  <>
                    <label>最近错误</label>
                    <div className="mono-box tone-danger">{p2pStatus.lastError}</div>
                  </>
                ) : null}
                <div className="action-row wrap">
                  <button className="primary" type="button" onClick={() => void handleP2PRuntimeStart()} disabled={p2pBusy || !p2pStatus?.available || Boolean(p2pStatus?.running)}>
                    {p2pBusy ? "处理中..." : p2pStatus?.running ? "EasyTier 运行中" : "启动 EasyTier"}
                  </button>
                  <button className="secondary" type="button" onClick={() => void handleP2PRuntimeStop()} disabled={p2pBusy || !p2pStatus?.running}>
                    停止 EasyTier
                  </button>
                  <button className="secondary" type="button" onClick={() => void refreshP2PStatus()} disabled={p2pBusy}>
                    刷新状态
                  </button>
                </div>
                <div className="action-row wrap">
                  <button className="secondary" type="button" onClick={() => void handleOpenP2PLog("stdout")}>
                    打开 stdout 日志
                  </button>
                  <button className="secondary" type="button" onClick={() => void handleOpenP2PLog("stderr")}>
                    打开 stderr 日志
                  </button>
                </div>
              </section>
            </div>
            <section className="card" style={{ marginTop: 18 }}>
              <h2>EasyTier 节点参数</h2>
              <p className="helper-text">
                这里配置的是用户端 EasyTier 节点本身，不是云端隧道。当前按官方命令行参数生成启动参数；保存只会落盘，不会自动重启 EasyTier。
              </p>
              <label>网络名</label>
              <input
                value={appConfig.p2pNetworkName || ""}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pNetworkName: event.target.value }))}
                placeholder="例如 cloud-relay"
              />
              <label>网络密钥</label>
              <input
                type="password"
                value={appConfig.p2pNetworkSecret || ""}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pNetworkSecret: event.target.value }))}
                placeholder="用于同一 EasyTier 网络鉴权"
              />
              <label>初始对等节点</label>
              <textarea
                rows={3}
                value={appConfig.p2pPeerUrl || ""}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pPeerUrl: event.target.value }))}
                placeholder={`${defaultP2PPeerUrl}\n可填多个，支持换行或逗号分隔`}
              />
              <p className="field-note">默认使用云端引导节点 `tcp://easytier.manage.020309.top:11010` 入网；如果后续新增长期在线的服务端节点，也可以一起填入。</p>
              <p className="field-note">用户端本地会固定监听 `21010` 的 `tcp/udp`，主动避开 EasyTier 默认的 `11010/11011/11012`，减少 Windows 端口权限冲突。</p>
              <label className="check-row">
                <input
                  type="checkbox"
                  checked={appConfig.p2pUseDhcp !== false}
                  onChange={(event) => setAppConfig((current) => ({ ...current, p2pUseDhcp: event.target.checked }))}
                />
                <span>使用 DHCP 自动分配虚拟 IPv4</span>
              </label>
              <label>固定虚拟 IPv4</label>
              <input
                value={appConfig.p2pVirtualIpv4 || ""}
                onChange={(event) => setAppConfig((current) => ({ ...current, p2pVirtualIpv4: event.target.value }))}
                placeholder="关闭 DHCP 时必填，例如 10.144.144.23"
                disabled={appConfig.p2pUseDhcp !== false}
              />
              <div className="grid two compact-grid">
                <div>
                  <label>实例名</label>
                  <input
                    value={appConfig.p2pInstanceName || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pInstanceName: event.target.value }))}
                    placeholder="默认 cloud-relay-user"
                  />
                </div>
                <div>
                  <label>主机名</label>
                  <input
                    value={appConfig.p2pHostname || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, p2pHostname: event.target.value }))}
                    placeholder="可选，便于识别用户端节点"
                  />
                </div>
              </div>
              <p className="inline-note">
                Windows 打包机会自动准备 `easytier-core.exe` 到安装包的 `runtime/` 目录中；当前页面会直接检查该文件是否已被带入。
              </p>
            </section>
          </>
        ) : null}

        {!initializing && user && page === "management" && managementVisible ? (
          <>
            <div className="page-header">
              <h1>{superManagementVisible ? "超级管理" : "管理页"}</h1>
              <p>
                {superManagementVisible
                  ? "超级管理员负责用户体系本身：公开注册、账号创建、普通管理员权限，以及普通用户的封禁解禁。"
                  : "普通管理员只负责日常用户值守：查看账号状态，并对普通用户做封禁或解禁。平台注册开关和管理员权限仍由超级管理员维护。"}
              </p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>{superManagementVisible ? "管理边界" : "普通管理员边界"}</h2>
                <div className="policy-chip">当前身份: {roleLabel(user.role)}</div>
                <div className="policy-chip">当前账号: {user.email}</div>
                <div className="policy-chip">公开注册: {superManagementVisible ? (authSettings.publicRegistrationEnabled ? "开启" : "关闭") : "仅超级管理员可见"}</div>
                <ul className="plain-list">
                  {superManagementVisible ? (
                    <>
                      <li>当前账号固定为唯一超级管理员，不允许删除、封禁或降级。</li>
                      <li>你可以创建普通用户和普通管理员，也可以把普通管理员降回普通用户。</li>
                      <li>公开注册开关只在超级管理员页出现，不再放在设置页。</li>
                    </>
                  ) : (
                    <>
                      <li>普通管理员只能查看账号列表，并对普通用户执行封禁或解禁。</li>
                      <li>普通管理员不能创建账号、不能改公开注册，也不能提升或降级管理员。</li>
                      <li>管理员账号和超级管理员账号都只能由超级管理员维护。</li>
                    </>
                  )}
                </ul>
              </section>
              <section className="card">
                <h2>{superManagementVisible ? "超级管理员密码" : "当前账号改密码"}</h2>
                {superManagementVisible ? (
                  <>
                    <div className="policy-chip">当前超级管理员账号已锁定自助改密</div>
                    <p className="helper-text">超级管理员密码不再提供邮箱验证码修改入口，只能通过你当前这种人工干预方式单独调整。</p>
                    <div className="mono-box">当前超级管理员账号不会在用户端页面中提供“发送验证码”或“验证并修改密码”功能。</div>
                  </>
                ) : (
                  <>
                    <p className="helper-text">验证码会发送到当前账号邮箱：{user.email}</p>
                    <label>邮箱验证码</label>
                    <div className="action-row wrap inline-form-row">
                      <input
                        value={passwordChangeForm.code}
                        onChange={(event) => setPasswordChangeForm((current) => ({ ...current, code: event.target.value }))}
                        placeholder="6 位验证码"
                      />
                      <button
                        className="secondary"
                        type="button"
                        onClick={() => void handleSendPasswordChangeCode()}
                        disabled={adminBusyAction === "send-password-code"}
                      >
                        {adminBusyAction === "send-password-code" ? "发送中..." : "发送验证码"}
                      </button>
                    </div>
                    <label>新密码</label>
                    <input
                      type="password"
                      value={passwordChangeForm.password}
                      onChange={(event) => setPasswordChangeForm((current) => ({ ...current, password: event.target.value }))}
                      placeholder="输入新密码"
                    />
                    <label>确认新密码</label>
                    <input
                      type="password"
                      value={passwordChangeForm.confirmPassword}
                      onChange={(event) => setPasswordChangeForm((current) => ({ ...current, confirmPassword: event.target.value }))}
                      placeholder="再次输入新密码"
                    />
                    <div className="action-row wrap">
                      <button
                        className="primary"
                        type="button"
                        onClick={() => void handleConfirmPasswordChange()}
                        disabled={adminBusyAction === "confirm-password-change"}
                      >
                        {adminBusyAction === "confirm-password-change" ? "修改中..." : "验证并修改密码"}
                      </button>
                    </div>
                  </>
                )}
              </section>
            </div>
            {superManagementVisible ? (
              <>
                <section className="card" style={{ marginTop: 18 }}>
                  <h2>公开注册</h2>
                  <div className="grid two compact-grid">
                    <div className="admin-block">
                      <h3>当前状态</h3>
                      <div className="policy-chip">{authSettings.publicRegistrationEnabled ? "公开注册已开启" : "公开注册已关闭"}</div>
                      <p className="inline-note">关闭后，普通用户不能自行注册，只能由超级管理员手工创建账号。</p>
                    </div>
                    <div className="admin-block">
                      <h3>注册开关</h3>
                      <p className="inline-note">该开关作用于整套用户端登录体系，不区分图床和网盘。</p>
                      <div className="action-row wrap">
                        <button
                          className="primary"
                          type="button"
                          onClick={() => void handleTogglePublicRegistration(!authSettings.publicRegistrationEnabled)}
                          disabled={adminBusyAction === "toggle-registration"}
                        >
                          {adminBusyAction === "toggle-registration"
                            ? "保存中..."
                            : (authSettings.publicRegistrationEnabled ? "关闭公开注册" : "开启公开注册")}
                        </button>
                      </div>
                    </div>
                  </div>
                </section>
                <section className="card" style={{ marginTop: 18 }}>
                  <h2>创建用户</h2>
                  <div className="grid two compact-grid">
                    <div>
                      <label>邮箱</label>
                      <input
                        value={managedUserForm.email}
                        onChange={(event) => setManagedUserForm((current) => ({ ...current, email: event.target.value }))}
                        placeholder="user@example.com"
                      />
                    </div>
                    <div>
                      <label>昵称</label>
                      <input
                        value={managedUserForm.displayName}
                        onChange={(event) => setManagedUserForm((current) => ({ ...current, displayName: event.target.value }))}
                        placeholder="显示名称"
                      />
                    </div>
                    <div>
                      <label>初始密码</label>
                      <input
                        type="password"
                        value={managedUserForm.password}
                        onChange={(event) => setManagedUserForm((current) => ({ ...current, password: event.target.value }))}
                        placeholder="输入初始密码"
                      />
                    </div>
                    <div>
                      <label>角色</label>
                      <select
                        value={managedUserForm.role}
                        onChange={(event) => setManagedUserForm((current) => ({ ...current, role: event.target.value as UserRole }))}
                      >
                        <option value="user">普通用户</option>
                        <option value="manager">普通管理员</option>
                      </select>
                    </div>
                  </div>
                  <div className="action-row wrap">
                    <button
                      className="primary"
                      type="button"
                      onClick={() => void handleCreateManagedUser()}
                      disabled={adminBusyAction === "create-user"}
                    >
                      {adminBusyAction === "create-user" ? "创建中..." : "创建用户"}
                    </button>
                  </div>
                </section>
              </>
            ) : (
              <section className="card" style={{ marginTop: 18 }}>
                <h2>超级管理员保留操作</h2>
                <ul className="plain-list">
                  <li>公开注册开关由超级管理员控制。</li>
                  <li>新账号创建和管理员权限调整由超级管理员执行。</li>
                  <li>普通管理员当前页只保留普通用户封禁解禁与自身邮箱验证码改密码。</li>
                </ul>
              </section>
            )}
            <section className="card" style={{ marginTop: 18 }}>
              <div className="service-header-row">
                <div>
                  <h2>用户管理</h2>
                  <p className="helper-text">
                    {superManagementVisible
                      ? "超级管理员可以维护普通用户与普通管理员；保留超级管理员账号不会在这里开放删除、封禁或降级。"
                      : "普通管理员可以查看全部账号，但只能封禁或解禁普通用户。"}
                  </p>
                </div>
                <button className="secondary" type="button" onClick={() => void refreshAdminData()} disabled={adminLoading}>
                  {adminLoading ? "刷新中..." : "刷新用户列表"}
                </button>
              </div>
              <div className="admin-user-list">
                {managedUsers.length === 0 ? (
                  <div className="service-empty">
                    <strong>暂无用户数据</strong>
                    <p>当前还没有可管理的用户记录。</p>
                  </div>
                ) : managedUsers.map((target) => {
                  const isCurrentAccount = target.id === user.id;
                  const roleEditable = canEditUserRole(user, target);
                  const disabledEditable = canToggleUserDisabled(user, target);
                  const roleDraft = userRoleDrafts[target.id] || target.role;
                  let stateDescription = "";
                  if (isCurrentAccount) {
                    stateDescription = "当前登录账号只能走邮箱验证码改密码，不允许在这里封禁、删除或修改自身角色。";
                  } else if (target.role === "admin") {
                    stateDescription = "保留超级管理员账号，由平台固定持有，不开放封禁、降级或删除。";
                  } else if (!superManagementVisible && target.role === "manager") {
                    stateDescription = "普通管理员账号由超级管理员维护，当前页只展示状态，不开放修改。";
                  } else if (target.disabled) {
                    stateDescription = "该用户已被封禁，现有会话会在服务端被拦截。";
                  } else if (!superManagementVisible && target.role === "user") {
                    stateDescription = "普通管理员可在当前页对该普通用户执行封禁或解禁。";
                  } else {
                    stateDescription = "该用户当前可正常登录和访问已授权服务。";
                  }

                  return (
                    <div key={target.id} className="admin-user-card">
                      <div className="admin-user-head">
                        <div>
                          <strong>{target.displayName}</strong>
                          <div className="helper-text small-text">{target.email}</div>
                        </div>
                        <div className="service-chip-row">
                          <span className="service-chip">{roleLabel(target.role)}</span>
                          <span className={`service-chip${target.disabled ? " danger-chip" : ""}`}>{userStateLabel(target)}</span>
                        </div>
                      </div>
                      <div className="grid two compact-grid">
                        <div>
                          <label>{superManagementVisible ? "角色维护" : "权限范围"}</label>
                          {roleEditable ? (
                            <select
                              value={roleDraft}
                              onChange={(event) => setUserRoleDrafts((current) => ({ ...current, [target.id]: event.target.value as UserRole }))}
                            >
                              <option value="user">普通用户</option>
                              <option value="manager">普通管理员</option>
                            </select>
                          ) : (
                            <div className="mono-box">
                              {target.role === "admin"
                                ? "超级管理员账号固定保留，不开放角色调整。"
                                : isCurrentAccount
                                  ? "当前登录账号不允许在这里改角色。"
                                  : (!superManagementVisible && target.role === "manager")
                                    ? "普通管理员不能调整其他管理员权限。"
                                    : "当前账号无需调整角色。"}
                            </div>
                          )}
                        </div>
                        <div>
                          <label>状态说明</label>
                          <div className="mono-box">{stateDescription}</div>
                        </div>
                      </div>
                      <div className="action-row wrap">
                        {superManagementVisible ? (
                          <button
                            className="primary"
                            type="button"
                            onClick={() => void handleSaveManagedUserRole(target)}
                            disabled={!roleEditable || adminBusyAction === "role:" + target.id}
                          >
                            {adminBusyAction === "role:" + target.id ? "保存中..." : "保存权限"}
                          </button>
                        ) : null}
                        <button
                          className={target.disabled ? "secondary" : "danger"}
                          type="button"
                          onClick={() => void handleToggleManagedUserDisabled(target)}
                          disabled={!disabledEditable || adminBusyAction === "toggle-user:" + target.id}
                        >
                          {adminBusyAction === "toggle-user:" + target.id
                            ? "处理中..."
                            : (target.disabled ? "解禁用户" : "封禁用户")}
                        </button>
                      </div>
                    </div>
                  );
                })}
              </div>
            </section>
          </>
        ) : null}

        {!initializing && user && page === "settings" ? (
          <>
            <div className="page-header">
              <h1>设置</h1>
              <p>设置页只保留用户端本地体验、连接地址和登录历史；账号管理已经移到独立管理页。</p>
            </div>
            <div className="grid two">
              <section className="card">
                <h2>连接与行为</h2>
                <label>云端地址</label>
                <input value={apiUrl} onChange={(event) => setApiUrl(event.target.value)} placeholder={defaultApiUrl} />
                <label>关闭行为</label>
                <select
                  value={appConfig.closeAction || defaultConfig.closeAction}
                  onChange={(event) => {
                    const next = { ...appConfig, closeAction: event.target.value as CloseAction };
                    setAppConfig(next);
                  }}
                >
                  <option value="ask">每次询问</option>
                  <option value="tray">最小化到托盘</option>
                  <option value="exit">直接退出</option>
                </select>
                <div className="state-row">
                  <span>静默启动</span>
                  <label style={{ display: "inline-flex", alignItems: "center", gap: 8, cursor: "pointer" }}>
                    <input
                      type="checkbox"
                      checked={Boolean(appConfig.silentStart)}
                      onChange={(event) => void handleSilentStartToggle(event.target.checked)}
                    />
                    <span>开机自启时隐藏到托盘</span>
                  </label>
                </div>
                <div className="state-row">
                  <span>开机启动</span>
                  <label style={{ display: "inline-flex", alignItems: "center", gap: 8, cursor: transport ? "pointer" : "not-allowed", opacity: transport ? 1 : 0.6 }}>
                    <input
                      type="checkbox"
                      checked={Boolean(appConfig.autoStart)}
                      disabled={!transport}
                      onChange={(event) => void handleAutoStartToggle(event.target.checked)}
                    />
                    <span>{transport ? "登录系统后自动启动用户端" : "仅桌面版可用"}</span>
                  </label>
                </div>
                <div className="action-row">
                  <button className="primary" type="button" onClick={() => void saveAndReconnect()} disabled={busy}>保存并重连</button>
                </div>
              </section>
              <section className="card">
                <h2>本地信息</h2>
                <div className="state-row"><span>配置目录</span><span>{desktopHostPaths.configDir}</span></div>
                <div className="state-row"><span>日志目录</span><span>{desktopHostPaths.logDir}</span></div>
                <div className="state-row"><span>当前账号</span><span>{user.email}</span></div>
                <div className="state-row"><span>当前角色</span><span>{roleLabel(user.role)}</span></div>
              </section>
            </div>
            <section className="card" style={{ marginTop: 18 }}>
              <h2>服务接入覆盖</h2>
              <p className="helper-text">优先使用云端目录里的服务入口；这里用于本机手填兜底或临时覆盖，不替代服务端正式登记。</p>
              <div className="grid two compact-grid">
                <div>
                  <label>网盘云端入口</label>
                  <input
                    value={appConfig.driveCloudUrl || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, driveCloudUrl: event.target.value }))}
                    placeholder="例如 https://drive.020309.top"
                  />
                  <label>网盘 P2P 入口</label>
                  <input
                    value={appConfig.driveP2pUrl || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, driveP2pUrl: event.target.value }))}
                    placeholder="例如 http://10.126.126.20:8080"
                  />
                </div>
                <div>
                  <label>图床云端入口</label>
                  <input
                    value={appConfig.galleryCloudUrl || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, galleryCloudUrl: event.target.value }))}
                    placeholder="例如 https://img.020309.top"
                  />
                  <label>图床 P2P 入口</label>
                  <input
                    value={appConfig.galleryP2pUrl || ""}
                    onChange={(event) => setAppConfig((current) => ({ ...current, galleryP2pUrl: event.target.value }))}
                    placeholder="例如 http://10.126.126.30:9090"
                  />
                </div>
              </div>
              <p className="inline-note">当云端目录里存在同 key 服务时，这里的地址只覆盖 URL，不覆盖云端分配的权限策略。</p>
            </section>
            {loginProfiles?.profiles.length ? (
              <section className="card" style={{ marginTop: 18 }}>
                <h2>登录历史</h2>
                {loginProfiles.profiles.map((profile) => (
                  <div key={profile.email} className="history-row">
                    <span>{profile.email}{profile.autoLogin ? " (自动登录)" : ""}</span>
                    <button className="danger small" type="button" onClick={() => setDeleteConfirmTarget(profile.email)}>移除</button>
                  </div>
                ))}
              </section>
            ) : null}
          </>
        ) : null}
      </main>

      {closeDialogOpen ? (
        <div className="modal-overlay" onClick={(event) => event.target === event.currentTarget && setCloseDialogOpen(false)}>
          <div className="modal-dialog">
            <h3>关闭确认</h3>
            <p>用户端关闭后，你仍可以在托盘中保持连接，或者直接退出应用。</p>
            <CloseDialogChoice onClick={handleCloseDialogChoice} onCancel={() => setCloseDialogOpen(false)} />
          </div>
        </div>
      ) : null}

      {reopenDialogOpen ? (
        <div className="modal-overlay" onClick={(event) => event.target === event.currentTarget && setReopenDialogOpen(false)}>
          <div className="modal-dialog">
            <h3>用户端已打开</h3>
            <p>当前已有一个用户端窗口。此版本先保持单窗口工作流。</p>
            <div className="close-dialog-buttons">
              <button className="primary" type="button" onClick={() => handleReopenChoice("cancel")}>知道了</button>
            </div>
          </div>
        </div>
      ) : null}

      {deleteConfirmTarget ? (
        <div className="modal-overlay" onClick={(event) => event.target === event.currentTarget && setDeleteConfirmTarget(null)}>
          <div className="modal-dialog">
            <h3>确认删除</h3>
            <p>确定删除账号 {deleteConfirmTarget} 的登录记录？</p>
            <div className="close-dialog-buttons">
              <button className="secondary dialog-btn" type="button" onClick={() => setDeleteConfirmTarget(null)}>取消</button>
              <button
                className="danger dialog-btn"
                type="button"
                onClick={() => {
                  const email = deleteConfirmTarget;
                  setDeleteConfirmTarget(null);
                  if (email) {
                    void handleDeleteProfile(email);
                  }
                }}
              >
                删除
              </button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function CloseDialogChoice({
  onClick,
  onCancel,
}: {
  onClick: (action: "tray" | "exit", dontAskAgain: boolean) => void;
  onCancel: () => void;
}) {
  const [dontAsk, setDontAsk] = useState(false);
  return (
    <div className="close-dialog-actions">
      <label className="close-dialog-remember">
        <input type="checkbox" checked={dontAsk} onChange={(event) => setDontAsk(event.target.checked)} />
        <span>以后不再询问</span>
      </label>
      <div className="close-dialog-buttons">
        <button className="primary" type="button" onClick={() => onClick("tray", dontAsk)}>最小化到托盘</button>
        <button className="secondary dialog-btn" type="button" onClick={() => onClick("exit", dontAsk)}>退出程序</button>
        <button className="secondary dialog-btn" type="button" onClick={onCancel}>取消</button>
      </div>
    </div>
  );
}
