import { expect, test } from "@playwright/test";
import { GATEWAY_URL } from "../playwright.config";

/**
 * gateway 服务的端到端用例。
 *
 * 这些用例不需要浏览器会话，用 request fixture 直接打公网入口 —— 它们要验证的是
 * 「网关这一层是否活着、鉴权是否 fail-close」，而不是页面渲染。
 */

test.describe("gateway", () => {
  test.use({ baseURL: GATEWAY_URL });

  test("存活探针可用", async ({ request }) => {
    const res = await request.get("/healthz");
    expect(res.status(), "网关没有在跑,或 HTTPRoute 的 backendRef 指向了不存在的 Service").toBe(
      200,
    );
  });

  test("就绪探针可用(路由表/JWT 公钥/Casbin 都已加载)", async ({ request }) => {
    // /readyz 是强判据:它为真才说明路由表拉到了、JWT 公钥装上了、resolver 有快照。
    // 只探 /healthz 会把「起来了但什么都没加载成」也算通过。
    const res = await request.get("/readyz");
    expect(res.status(), `就绪失败,响应体:${(await res.text()).slice(0, 200)}`).toBe(200);
  });

  // ⚠️ 网关按「一级 proto 包名」路由(/order.…、/product.…),不是 REST 路径。
  // 拿 /api/v1/orders 这种路径去测只会得到 404,看上去像鉴权没生效。
  test("受保护 RPC 匿名访问必须 fail-close(401/403,不能是 5xx 或放行)", async ({ request }) => {
    const res = await request.post("/order.v1.OrderService/ListOrders", {
      data: {},
      failOnStatusCode: false,
    });
    expect(
      [401, 403],
      `匿名访问受保护 RPC 返回了 ${res.status()} —— 2xx 是鉴权被绕过,5xx 是网关自身出错,404 多半是路径写错`,
    ).toContain(res.status());
  });

  test("匿名清单里的 RPC 必须放行(不能被一刀切拦掉)", async ({ request }) => {
    // routes/{dev,pre}.yaml 的 anonymous 清单:未登录用户也要能查行政区划。
    // 这条与上一条互为对照 —— 只测「该拦的拦了」证明不了清单还生效。
    const res = await request.post("/address.v1.RegionService/ListRegions", {
      data: {},
      failOnStatusCode: false,
    });
    expect(res.status(), "匿名清单失效了,公开只读接口被鉴权拦住").toBe(200);
  });

  test("配置中心的 RPC 不经网关暴露", async ({ request }) => {
    // 边界:网关只代理业务路由,/config.v1.* 不在路由表里,应当 404 而不是被转发出去。
    const res = await request.post("/config.v1.ConfigService/GetKey", {
      data: {},
      failOnStatusCode: false,
    });
    expect([404, 405]).toContain(res.status());
  });
});
