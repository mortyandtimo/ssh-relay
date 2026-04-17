import { startTransition, useCallback, useEffect, useMemo, useState } from "react";
import type { MouseEvent } from "react";
import { listen } from "@tauri-apps/api/event";
import { createDesktopApi } from "../../../packages/desktop-core/src/api";
import type { UserServiceEntry, UserSummary } from "../../../packages/desktop-core/src/types";
import {
  appExit,
  createTauriDesktopTransport,
  decryptLoginPassword,
  deleteLoginProfile,
  loadAppConfig,
  loadP2PRuntimeStatus,
  loadDesktopHostPaths,
  openExternal,
  openP2PRuntimeLog,
  readLoginProfiles,
  saveAppConfig,
  saveLoginProfile,
  startP2PRuntime,
  stopP2PRuntime,
  windowMinimize,
  windowRequestClose,
  windowStartDrag,
  windowToggleMaximize,
  type AppConfig,
  type LoginProfilesFile,
  type P2PRuntimeStatus,
} from "./desktopHost";

type CloseAction = "ask" | "tray" | "exit";
type DriveFallbackPolicy = "admin_only" | "never";
type ImageBulkUploadMode = "p2p_bulk_https_light" | "https_only";
type Page = "loading" | "login" | "home" | "drive" | "gallery" | "p2p" | "settings";
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
const defaultConfig: Required<Pick<AppConfig, "closeAction" | "driveFallbackPolicy" | "imageBulkUploadMode" | "p2pUseDhcp">> = {
  closeAction: "ask",
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

function mergeConfig(config?: AppConfig | null): AppConfig {
  return {
    apiBaseUrl: normalizeApiBaseUrl(config?.apiBaseUrl),
    closeAction: (config?.closeAction || defaultConfig.closeAction) as CloseAction,
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

function p2pRuntimeLabel(status?: P2PRuntimeStatus | null) {
  if (!status) return "读取中";
  if (status.running && status.peerCount > 0) return `已联网 (${status.peerCount})`;
  if (status.running) return "运行中";
  if (!status.available) return "未打包 easytier-core";
  if (!status.configured) return "待配置";
  return "已停止";
}

function roleLabel(role?: string) {
  switch (role) {
    case "admin":
      return "管理员";
    case "manager":
      return "管理用户";
    case "user":
      return "普通用户";
    default:
      return "未登录";
  }
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
  const [userServices, setUserServices] = useState<UserServiceEntry[]>([]);
  const [serviceLoading, setServiceLoading] = useState(false);

  const [apiUrl, setApiUrl] = useState(defaultApiUrl);
  const [loginEmail, setLoginEmail] = useState("");
  const [loginPassword, setLoginPassword] = useState("");
  const [savePassword, setSavePassword] = useState(false);
  const [autoLogin, setAutoLogin] = useState(false);

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
      return true;
    } catch {
      setUser(null);
      setUserServices([]);
      return false;
    }
  }, [api, refreshUserServices]);

  useEffect(() => {
    let cancelled = false;
    void (async () => {
      try {
        const [config, profiles, hostPaths] = await Promise.all([
          loadAppConfig(),
          readLoginProfiles(),
          loadDesktopHostPaths(),
        ]);
        if (cancelled) return;

        const mergedConfig = mergeConfig(config);
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
          setPage("login");
        });
        setInitializing(false);
        void refreshP2PStatus();

        const sessionApi = createDesktopApi(nextApiUrl, transport || undefined);
        const authed = await sessionApi.loadCurrentUser()
          .then((response) => {
            if (cancelled) return true;
            startTransition(() => {
              setUser(response.user);
              setPage("home");
              setError("");
            });
            void refreshUserServices(sessionApi);
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
                setPage("home");
                setError("");
              });
              void refreshUserServices(sessionApi);
            }
          } catch {
            if (!cancelled) {
              startTransition(() => {
                setPage("login");
              });
            }
          }
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
  }, [refreshP2PStatus, refreshUserServices, transport]);

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
      await refreshUserServices(loginApi);
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
  }, [apiUrl, appConfig, autoLogin, loginEmail, loginPassword, persistConfig, refreshLoginProfiles, refreshUserServices, savePassword, transport]);

  const handleLogout = useCallback(async () => {
    setBusy(true);
    try {
      await api.logout();
    } catch {
      // ignore logout transport errors
    } finally {
      setUser(null);
      setUserServices([]);
      setPage("login");
      setBusy(false);
    }
  }, [api]);

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
    const timer = window.setInterval(() => {
      void refreshP2PStatus();
    }, 5000);
    return () => {
      window.clearInterval(timer);
    };
  }, [initializing, page, refreshP2PStatus]);

  const saveP2PSettings = useCallback(async () => {
    setP2PBusy(true);
    setError("");
    try {
      const merged = mergeConfig(appConfig);
      setAppConfig(merged);
      await saveAppConfig(merged);
      await refreshP2PStatus();
      setNotice("P2P 设置已保存。当前不会自动重启；若 EasyTier 已在运行，请手动停止后再启动以应用新参数。");
    } catch (configError) {
      setError(configError instanceof Error ? configError.message : "保存 P2P 设置失败");
    } finally {
      setP2PBusy(false);
    }
  }, [appConfig, refreshP2PStatus]);

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
      setNotice(status.peerCount > 0 ? `EasyTier 已启动并接入 ${status.peerCount} 个对等节点。` : "EasyTier 已启动。");
    } catch (startError) {
      setError(startError instanceof Error ? startError.message : "启动 EasyTier 失败");
    } finally {
      setP2PBusy(false);
    }
  }, [appConfig, p2pStatus?.running]);

  const handleP2PRuntimeStop = useCallback(async () => {
    setP2PBusy(true);
    setError("");
    setNotice("");
    try {
      const status = await stopP2PRuntime();
      setP2PStatus(status);
      setNotice("EasyTier 已停止。");
    } catch (stopError) {
      setError(stopError instanceof Error ? stopError.message : "停止 EasyTier 失败");
    } finally {
      setP2PBusy(false);
    }
  }, []);

  const handleOpenP2PLog = useCallback(async (kind: "stdout" | "stderr") => {
    try {
      await openP2PRuntimeLog(kind);
    } catch (logError) {
      setError(logError instanceof Error ? logError.message : "打开日志失败");
    }
  }, []);

  const handleOpenServiceUrl = useCallback(async (url: string, label: string) => {
    try {
      await openExternal(url);
    } catch (openError) {
      setError(openError instanceof Error ? openError.message : `打开${label}失败`);
    }
  }, []);

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
      <h2>登录用户端</h2>
      <p className="helper-text">用户端用于访问网盘、执行批量图床上传、接入 P2P 网络。云端仍负责鉴权、目录和公开入口。</p>
      <label>云端地址</label>
      <input value={apiUrl} onChange={(event) => setApiUrl(event.target.value)} placeholder={defaultApiUrl} />
      <label>邮箱</label>
      {loginProfiles?.profiles.length ? (
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
      <label>密码</label>
      <input type="password" value={loginPassword} onChange={(event) => setLoginPassword(event.target.value)} />
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
        <button className="primary" type="button" onClick={() => void handleLogin()} disabled={busy}>
          {busy ? "登录中..." : "登录"}
        </button>
        <button className="secondary" type="button" onClick={() => void saveAndReconnect()} disabled={busy}>
          保存地址
        </button>
      </div>
    </div>
  );

  const driveFallbackLabel = (appConfig.driveFallbackPolicy || defaultConfig.driveFallbackPolicy) === "admin_only"
    ? "仅管理员允许云端回退下载"
    : "禁止云端回退，必须直连 P2P";
  const imageUploadLabel = (appConfig.imageBulkUploadMode || defaultConfig.imageBulkUploadMode) === "p2p_bulk_https_light"
    ? "批量走 P2P，轻量走 HTTPS"
    : "全部走 HTTPS";
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
            <button className={`nav-btn ${page === "home" ? "active" : ""}`} type="button" onClick={() => setPage("home")}>总览</button>
            <button className={`nav-btn ${page === "drive" ? "active" : ""}`} type="button" onClick={() => setPage("drive")}>网盘</button>
            <button className={`nav-btn ${page === "gallery" ? "active" : ""}`} type="button" onClick={() => setPage("gallery")}>图床</button>
            <button className={`nav-btn ${page === "p2p" ? "active" : ""}`} type="button" onClick={() => setPage("p2p")}>P2P 网络</button>
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
              <h1>用户端总览</h1>
              <p>区分三端：云端负责控制面与公网入口，服务端承载网盘/图床，用户端负责访问与 P2P 直连。</p>
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
                <div className="policy-chip">网盘: {driveFallbackLabel}</div>
                <div className="policy-chip">图床上传: {imageUploadLabel}</div>
                <div className="policy-chip">图床读取: 保持云端 HTTPS 公网入口</div>
                <div className="policy-chip">P2P 随应用启动: {appConfig.p2pAutoStart ? "已开启" : "未开启"}</div>
                <div className="policy-chip">EasyTier 运行态: {p2pRuntimeLabel(p2pStatus)}</div>
                <div className="policy-chip">本机节点: {p2pStatus?.nodeHostname || "待上报"}</div>
                <div className="policy-chip">虚拟 IPv4: {p2pStatus?.virtualIpv4 || "未分配"}</div>
                <div className="policy-chip">当前对等节点: {p2pStatus?.peerCount ?? 0}</div>
              </section>
            </div>
            <section className="card" style={{ marginTop: 18 }}>
              <div className="service-header-row">
                <div>
                  <h2>服务工作台</h2>
                  <p className="helper-text">这里汇总云端目录里登记的双入口服务，以及本机手填的兜底入口。</p>
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
                    <span className="service-chip">{driveService?.publicUrl ? "云端已接入" : "云端待配置"}</span>
                    <span className="service-chip">{driveService?.p2pUrl ? "P2P 已接入" : "P2P 待配置"}</span>
                  </div>
                </div>
                <div className="service-tile">
                  <div className="service-tile-top">
                    <strong>图床</strong>
                    <span className="service-badge">{galleryService ? serviceSourceLabel(galleryService.source) : "未配置"}</span>
                  </div>
                  <p>{galleryService?.summary || "尚未在云端目录或本地设置中登记图床入口。"}</p>
                  <div className="service-chip-row">
                    <span className="service-chip">{galleryService?.publicUrl ? "HTTPS 已接入" : "云端待配置"}</span>
                    <span className="service-chip">{galleryService?.p2pUrl ? "批量 P2P 已接入" : "P2P 待配置"}</span>
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
                      <span className="service-chip">{service.publicUrl ? "云端" : "无云端入口"}</span>
                      <span className="service-chip">{service.p2pUrl ? "P2P" : "无 P2P 入口"}</span>
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
              <p>用户端面向大文件下载。目标是 P2P 直连优先，不默认回退到云端中继。</p>
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
                    <div className="policy-chip">首选路径: {preferredPathLabel(driveService.preferredPath)}</div>
                    <div className="policy-chip">云端权限: {serviceAccessLabel(driveService.cloudAccess)}</div>
                    <div className="policy-chip">P2P 权限: {serviceAccessLabel(driveService.p2pAccess)}</div>
                    <label>云端入口</label>
                    <div className="mono-box">{driveService.publicUrl || "未配置"}</div>
                    <div className="action-row wrap">
                      <button
                        className="primary"
                        type="button"
                        onClick={() => void handleOpenServiceUrl(driveService.publicUrl, "网盘云端入口")}
                        disabled={!driveService.publicUrl || !driveService.cloudAllowed}
                      >
                        打开云端入口
                      </button>
                      <button
                        className="secondary"
                        type="button"
                        onClick={() => void handleCopyValue(driveService.publicUrl, "网盘云端地址")}
                        disabled={!driveService.publicUrl}
                      >
                        复制云端地址
                      </button>
                    </div>
                    <label>P2P 入口</label>
                    <div className="mono-box">{driveService.p2pUrl || "未配置"}</div>
                    <div className="action-row wrap">
                      <button
                        className="primary"
                        type="button"
                        onClick={() => void handleOpenServiceUrl(driveService.p2pUrl, "网盘 P2P 入口")}
                        disabled={!driveService.p2pUrl || !driveService.p2pAllowed}
                      >
                        打开 P2P 入口
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
                <div className="state-row"><span>云端回退</span><span>{driveFallbackLabel}</span></div>
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
                <li>普通用户没有 P2P 入口时，不默认开放云端大文件下载。</li>
                <li>管理员可以保留受控云端回退，用于维护和核对。</li>
                <li>目录、分享页、鉴权信息继续由云端提供，真正的大流量下载优先走服务端 P2P。</li>
              </ul>
            </section>
          </>
        ) : null}

        {!initializing && user && page === "gallery" ? (
          <>
            <div className="page-header">
              <h1>图床</h1>
              <p>图床拆成两条路径：轻量日常操作保留 HTTPS，批量上传通过专门的 P2P 入口走服务端。</p>
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
                    <div className="policy-chip">首选路径: {preferredPathLabel(galleryService.preferredPath)}</div>
                    <label>公开 / 轻量 HTTPS 入口</label>
                    <div className="mono-box">{galleryService.publicUrl || "未配置"}</div>
                    <div className="action-row wrap">
                      <button
                        className="primary"
                        type="button"
                        onClick={() => void handleOpenServiceUrl(galleryService.publicUrl, "图床云端入口")}
                        disabled={!galleryService.publicUrl || !galleryService.cloudAllowed}
                      >
                        打开 HTTPS 入口
                      </button>
                      <button
                        className="secondary"
                        type="button"
                        onClick={() => void handleCopyValue(galleryService.publicUrl, "图床云端地址")}
                        disabled={!galleryService.publicUrl}
                      >
                        复制 HTTPS 地址
                      </button>
                    </div>
                    <label>批量上传 P2P 入口</label>
                    <div className="mono-box">{galleryService.p2pUrl || "未配置"}</div>
                    <div className="action-row wrap">
                      <button
                        className="primary"
                        type="button"
                        onClick={() => void handleOpenServiceUrl(galleryService.p2pUrl, "图床 P2P 入口")}
                        disabled={!galleryService.p2pUrl || !galleryService.p2pAllowed}
                      >
                        打开 P2P 入口
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
                <div className="state-row"><span>图床上传策略</span><span>{imageUploadLabel}</span></div>
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
                <li>博客图片、日常浏览、外链展示继续使用云端 HTTPS。</li>
                <li>批量上传单独走 P2P 入口，避免把高流量图片上传压在云端。</li>
                <li>读取链路和上传链路明确分开，后续新增其他服务时也沿用同一套双入口模型。</li>
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
                <label>网盘回退策略</label>
                <select
                  value={appConfig.driveFallbackPolicy || defaultConfig.driveFallbackPolicy}
                  onChange={(event) => setAppConfig((current) => ({ ...current, driveFallbackPolicy: event.target.value as DriveFallbackPolicy }))}
                >
                  <option value="admin_only">仅管理员允许云端回退</option>
                  <option value="never">禁止云端回退</option>
                </select>
                <label>图床批量上传策略</label>
                <select
                  value={appConfig.imageBulkUploadMode || defaultConfig.imageBulkUploadMode}
                  onChange={(event) => setAppConfig((current) => ({ ...current, imageBulkUploadMode: event.target.value as ImageBulkUploadMode }))}
                >
                  <option value="p2p_bulk_https_light">批量走 P2P，轻量走 HTTPS</option>
                  <option value="https_only">全部走 HTTPS</option>
                </select>
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

        {!initializing && user && page === "settings" ? (
          <>
            <div className="page-header">
              <h1>设置</h1>
              <p>用户端只管理用户侧访问、P2P 和本地体验，不承担服务端发布工作。</p>
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
