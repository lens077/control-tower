import { createClient } from "@connectrpc/connect";
import { ConfigService, ConfigFormat } from "@/gen/api";
import { createAppTransport } from "@/api/transport";

const transport = createAppTransport();

const client = createClient(ConfigService, transport);

export type PresenceMode = "local" | "redis_ttl" | "redis_ttl_degraded";

function presenceMode(value: string | null): PresenceMode {
  switch (value) {
    case "redis_ttl":
    case "redis_ttl_degraded":
      return value;
    default:
      return "local";
  }
}

export interface PutKeyInput {
  namespace: string;
  environment: string;
  key: string;
  format: ConfigFormat;
  value: string;
  comment?: string;
  isSecret?: boolean;
  description?: string;
}

export const configApi = {
  // Use the raw transport for this one endpoint because the server advertises
  // its observation scope in a CORS-exposed response header, not in config data.
  listClientConnections: async (signal?: AbortSignal) => {
    const response = await transport.unary(
      ConfigService.method.listClientConnections,
      signal,
      undefined,
      undefined,
      {},
    );
    return {
      connections: response.message.connections,
      presenceMode: presenceMode(response.header.get("x-config-center-presence-mode")),
    };
  },

  listNamespaces: (signal?: AbortSignal) => client.listNamespaces({}, { signal }),

  listKeys: (namespace: string, environment: string, keyPrefix = "", signal?: AbortSignal) =>
    client.listKeys({ namespace, environment, keyPrefix }, { signal }),

  getKey: (namespace: string, environment: string, key: string, signal?: AbortSignal) =>
    client.getKey({ namespace, environment, key }, { signal }),

  putKey: (input: PutKeyInput, signal?: AbortSignal) =>
    client.putKey(
      {
        namespace: input.namespace,
        environment: input.environment,
        key: input.key,
        format: input.format,
        value: input.value,
        comment: input.comment ?? "",
        isSecret: input.isSecret ?? false,
        description: input.description ?? "",
      },
      { signal },
    ),

  deleteKey: (namespace: string, environment: string, key: string, signal?: AbortSignal) =>
    client.deleteKey({ namespace, environment, key }, { signal }),

  listRevisions: (namespace: string, environment: string, key: string, signal?: AbortSignal) =>
    client.listRevisions({ namespace, environment, key }, { signal }),

  getRevision: (
    namespace: string,
    environment: string,
    key: string,
    version: number,
    signal?: AbortSignal,
  ) => client.getRevision({ namespace, environment, key, version }, { signal }),

  rollback: (
    namespace: string,
    environment: string,
    key: string,
    version: number,
    comment = "",
    signal?: AbortSignal,
  ) => client.rollback({ namespace, environment, key, version, comment }, { signal }),
};

export { ConfigFormat };
