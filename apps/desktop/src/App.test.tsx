import { renderToStaticMarkup } from "react-dom/server";
import { describe, expect, it } from "vitest";

import App from "./App";

describe("desktop application shell", () => {
  it("renders task pages instead of the former marketing homepage", () => {
    const markup = renderToStaticMarkup(<App />);

    expect(markup).toContain("主要导航");
    expect(markup).toContain("概览");
    expect(markup).toContain("提供商");
    expect(markup).toContain("令牌");
    expect(markup).toContain("路由");
    expect(markup).toContain("Agent 工具");
    expect(markup).toContain("设置");
    expect(markup).toContain("usage-heading");
    expect(markup).toContain("连接中");
    expect(markup).toContain("用量概览");
    expect(markup).toContain("请求");
    expect(markup).toContain("系统详情");
    expect(markup).not.toContain("欢迎使用 AstrLink");
    expect(markup).not.toContain("当前为浏览器预览");

    expect(markup).not.toContain("统一 API 网关");
    expect(markup).not.toContain("运行在你的桌面");
    expect(markup).not.toContain("本地 AI 网关");
    expect(markup).not.toContain("Alpha 将使用");
    expect(markup).not.toContain("新手引导");
    expect(markup).not.toContain("尚未添加 API 提供商");
  });
});
