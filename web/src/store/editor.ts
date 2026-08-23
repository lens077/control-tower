import { proxy, subscribe } from "valtio";

// 编辑器/浏览器上下文:当前选择的命名空间、环境与 key。
export interface EditorState {
  namespace: string;
  environment: string;
  selectedKey: string | null;
  dirty: boolean;
}

// 上次选择的上下文;损坏的 localStorage 不应让整个应用白屏。
function readSaved(): Partial<Pick<EditorState, "namespace" | "environment">> {
  try {
    const raw = localStorage.getItem("config_editor_ctx");
    return raw ? JSON.parse(raw) : {};
  } catch {
    return {};
  }
}

const initial = readSaved();

// namespace 不设默认值:由 ListNamespaces 返回的真实列表决定(见 routes/index.tsx),
// 避免写死一个配置中心里并不存在的 namespace 导致列表恒为空。
export const editorStore = proxy<EditorState>({
  namespace: initial.namespace || "",
  environment: initial.environment || "dev",
  selectedKey: null,
  dirty: false,
});

export const setNamespace = (ns: string) => {
  editorStore.namespace = ns;
};
export const setEnvironment = (env: string) => {
  editorStore.environment = env;
};
export const setSelectedKey = (key: string | null) => {
  editorStore.selectedKey = key;
};
export const setDirty = (v: boolean) => {
  editorStore.dirty = v;
};

// 仅持久化 namespace/environment
subscribe(editorStore, () => {
  localStorage.setItem(
    "config_editor_ctx",
    JSON.stringify({ namespace: editorStore.namespace, environment: editorStore.environment }),
  );
});
