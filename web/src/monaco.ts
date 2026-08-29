/**
 * Monaco 资源必须自托管。
 *
 * `@monaco-editor/react` 默认从 `cdn.jsdelivr.net` 注入 AMD `loader.js`,而生产镜像
 * 的 CSP 是 `script-src 'self'`(见 `web/Dockerfile`)—— 跨域脚本会被浏览器直接拦掉。
 * 这个故障模式非常隐蔽:
 *
 *   1. CSP 拦截发生在发请求之前,网络面板里看不到任何失败条目;
 *   2. `@monaco-editor/react` 只把 init 失败 `console.error` 出来,不改渲染状态,
 *      于是 `<Editor>` / `<DiffEditor>` 永远停在它的兜底占位符 "Loading..."。
 *
 * 结果就是 /edit 与 /history 页面「一直 Loading、控制台网络台都看不出错」。所以这里
 * 显式把 loader 指向同源路径;`/vs` 由 vite.config.ts 的 monaco-self-host 插件提供
 * (dev 走中间件直读 node_modules,build 时拷进 dist/vs)。
 *
 * 必须在任何 Editor 挂载(即 `loader.init()`)之前执行,所以由入口 main.tsx 静态导入。
 */
import { loader } from "@monaco-editor/react";

/** 同源的 monaco AMD 资源根路径。BASE_URL 默认为 "/",即 "/vs"。 */
export const MONACO_VS_PATH = `${import.meta.env.BASE_URL}vs`;

loader.config({ paths: { vs: MONACO_VS_PATH } });
