import { invoke } from "@tauri-apps/api/core";

export type LoginProfile = {
  email: string;
  encryptedPassword: string;
  autoLogin: boolean;
};

export type LoginProfilesFile = {
  profiles: LoginProfile[];
  lastUsedEmail: string | null;
};

export type AppConfig = {
  apiBaseUrl?: string;
  closeAction?: "ask" | "tray" | "exit";
};

export async function readLoginProfiles(): Promise<LoginProfilesFile | null> {
  try {
    return await invoke<LoginProfilesFile>("read_login_profiles");
  } catch {
    return null;
  }
}

export async function saveLoginProfile(email: string, password: string, autoLogin: boolean): Promise<void> {
  await invoke("save_login_profile", { email, password, autoLogin });
}

export async function deleteLoginProfile(email: string): Promise<void> {
  await invoke("delete_login_profile", { email });
}

export async function decryptLoginPassword(email: string): Promise<string> {
  return await invoke<string>("decrypt_login_password", { email });
}

export async function saveAppConfig(config: AppConfig): Promise<void> {
  await invoke("save_app_config", { config });
}

export async function loadAppConfig(): Promise<AppConfig | null> {
  try {
    return await invoke<AppConfig>("load_app_config");
  } catch {
    return null;
  }
}
