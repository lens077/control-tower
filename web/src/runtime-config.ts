export interface RuntimeConfig {
  apiUrl: string;
  casdoor: {
    serverUrl: string;
    clientId: string;
    organizationName: string;
    appName: string;
    redirectPath: string;
  };
}

interface RawRuntimeConfig extends Omit<RuntimeConfig, "apiUrl"> {
  apiUrl?: string;
  gatewayUrl?: string;
}

let config: RuntimeConfig | undefined;

// config.json is mounted beside the static files, so changing the API or public
// OIDC values does not require rebuilding the Web console.
export async function loadRuntimeConfig(): Promise<RuntimeConfig> {
  const response = await fetch(`${import.meta.env.BASE_URL}config.json`, { cache: "no-store" });
  if (!response.ok) {
    throw new Error(`load runtime configuration: ${response.status}`);
  }

  const parsed = (await response.json()) as RawRuntimeConfig;
  const apiUrl = parsed.apiUrl ?? parsed.gatewayUrl;
  if (!parsed.apiUrl && parsed.gatewayUrl) {
    console.warn("[Config] gatewayUrl 已弃用，请把运行时配置迁移到直连 API 的 apiUrl");
  }
  if (!apiUrl || !parsed.casdoor?.serverUrl || !parsed.casdoor.clientId) {
    throw new Error("runtime configuration is incomplete");
  }

  config = { apiUrl, casdoor: parsed.casdoor };
  return config;
}

export function getRuntimeConfig(): RuntimeConfig {
  if (!config) {
    throw new Error("runtime configuration has not been loaded");
  }
  return config;
}
