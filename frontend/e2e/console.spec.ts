import { expect, test } from "@playwright/test";
import type { Page } from "@playwright/test";

const user = {
  id: 7,
  name: "测试用户",
  email: "console@example.test",
  role: "user",
  currency: "CNY",
  balance: 8_000_000,
};
const csrf = "test-csrf";

async function stubAPI(
  page: Page,
  handler: (url: URL, body: any, method: string) => unknown,
  signIn = true,
) {
  await page.route("**/api/**", async (route) => {
    const request = route.request();
    const url = new URL(request.url());
    if (url.pathname === "/api/auth/me") {
      await route.fulfill({
        status: signIn ? 200 : 401,
        json: signIn ? { user, csrf } : { error: "请先登录" },
      });
      return;
    }
    if (url.pathname === "/api/auth/config") {
      await route.fulfill({ json: { email_enabled: true } });
      return;
    }
    const body = request.postData() ? request.postDataJSON() : undefined;
    const json = handler(url, body, request.method());
    if (json === undefined) {
      await route.fulfill({
        status: 404,
        json: { error: "Unexpected test route" },
      });
    } else {
      await route.fulfill({ json });
    }
  });
}

test("login submits credentials and takes the user to their workspace", async ({
  page,
}) => {
  await stubAPI(
    page,
    (url, body, method) => {
      if (url.pathname === "/api/auth/login") {
        expect(method).toBe("POST");
        expect(body).toMatchObject({
          email: user.email,
          password: "test-password",
        });
        return { user, csrf };
      }
      if (url.pathname === "/api/keys" || url.pathname === "/api/tools")
        return [];
    },
    false,
  );

  await page.goto("/login");
  await page.getByLabel("邮箱地址").fill(user.email);
  await page.getByLabel("密码", { exact: true }).fill("test-password");
  await page.getByRole("button", { name: "登录", exact: true }).click();
  await expect(page).toHaveURL(/\/app(\/|$)/);
  await expect(page.getByRole("heading", { name: /欢迎来到/ })).toBeVisible();
});

test("history applies server-side filters and preserves them across pages", async ({
  page,
}) => {
  const queries: URLSearchParams[] = [];
  await stubAPI(page, (url) => {
    if (url.pathname !== "/api/usage") return;
    queries.push(url.searchParams);
    const cursor = url.searchParams.get("cursor");
    const status = url.searchParams.get("status");
    return {
      items: [
        {
          id: cursor ? 12 : 13,
          status: status ?? "success",
          tool_name: cursor ? "第二页工具" : "第一页工具",
          endpoint: "/search",
          cost: 100_000,
          duration_ms: 20,
          created_at: "2026-11-10T12:00:00Z",
        },
      ],
      next_cursor: cursor ? "" : "13",
    };
  });

  await page.goto("/app/usage");
  await expect(page.getByText("第一页工具")).toBeVisible();
  await page.getByLabel("调用状态").selectOption("unknown");
  await page.getByRole("button", { name: "筛选" }).click();
  await expect(page.getByRole("link", { name: "导出 CSV" })).toHaveAttribute(
    "href",
    /status=unknown&format=csv/,
  );
  await page.getByRole("button", { name: "下一页" }).click();
  await expect(page.getByText("第二页工具")).toBeVisible();
  expect(queries.at(-1)?.get("status")).toBe("unknown");
  expect(queries.at(-1)?.get("cursor")).toBe("13");
  await page.getByRole("button", { name: "上一页" }).click();
  await expect(page.getByText("第一页工具")).toBeVisible();
});

test("API key creation sends selected tool permissions and budget", async ({
  page,
}) => {
  await stubAPI(page, (url, body, method) => {
    if (url.pathname === "/api/tools") {
      return [
        {
          id: 42,
          name: "搜索供应商",
          endpoints: [
            { id: "search", name: "网页搜索", tool_ref: "t42.c2VhcmNo" },
          ],
        },
      ];
    }
    if (url.pathname === "/api/keys") {
      if (method === "GET") return [];
      expect(body).toEqual({
        label: "Search Agent",
        daily_limit_micros: 2_500_000,
        allowed_tools: ["t42.c2VhcmNo"],
      });
      return { id: 81, key: "tv_live_test_key" };
    }
  });

  await page.goto("/app/keys");
  await page.getByRole("button", { name: /创建/ }).first().click();
  await page.getByLabel("名称", { exact: true }).fill("Search Agent");
  await page.getByLabel(/每日预算/).fill("2.5");
  await page.getByLabel(/可用工具/).selectOption("t42.c2VhcmNo");
  await page.getByRole("button", { name: "创建密钥", exact: true }).click();
  await expect(page.getByRole("heading", { name: "密钥已创建" })).toBeVisible();
  await expect(
    page.getByRole("textbox", { name: /新建的 API 密钥/ }),
  ).toHaveValue("tv_live_test_key");
});
