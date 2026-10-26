import { expect, it } from "vitest";
import { parseServiceTestResult } from "./service-test-model";

const result = {
  service_id: "service_test",
  protocol: "openai.chat",
  model: "test",
  stream: true,
  ok: true,
  status_code: 200,
  duration_ms: 300,
  output: "OK",
};

it("accepts measured zero, nullable timings and older Core responses", () => {
  expect(parseServiceTestResult(result).first_token_ms).toBeUndefined();
  expect(
    parseServiceTestResult({
      ...result,
      response_headers_ms: null,
      first_token_ms: null,
    }).first_token_ms,
  ).toBeNull();
  expect(
    parseServiceTestResult({
      ...result,
      response_headers_ms: 0,
      first_token_ms: 0,
    }).first_token_ms,
  ).toBe(0);
  expect(
    parseServiceTestResult({
      ...result,
      response_headers_ms: 30,
      first_token_ms: 200,
    }).first_token_ms,
  ).toBe(200);
});

it("rejects invalid timing data at the bridge boundary", () => {
  for (const timing of [
    { response_headers_ms: -1 },
    { response_headers_ms: "30" },
    { response_headers_ms: NaN },
    { response_headers_ms: 301 },
    { response_headers_ms: 0.5 },
    { first_token_ms: 20 },
    { response_headers_ms: 30, first_token_ms: 20 },
    { response_headers_ms: 30, first_token_ms: 400 },
    { response_headers_ms: 30, first_token_ms: 40, stream: false },
  ]) {
    expect(() => parseServiceTestResult({ ...result, ...timing })).toThrow(
      "Invalid provider test timing",
    );
  }
});

it("accepts raw bodies without parsing them and rejects invalid envelope field types", () => {
  const raw = "data: malformed <script>bad()</script>\r\n\r\n";
  expect(
    parseServiceTestResult({
      ...result,
      raw_response: raw,
      raw_response_truncated: true,
      response_content_type: "text/event-stream",
    }).raw_response,
  ).toBe(raw);
  for (const fields of [
    { raw_response: {} },
    { raw_response_truncated: "true" },
    { response_content_type: 1 },
  ]) {
    expect(() => parseServiceTestResult({ ...result, ...fields })).toThrow(
      "Invalid provider test result",
    );
  }
});

it("reads what a task plugin's provider generated, only as the gateway's files", () => {
  const file = "/v1/tasks/resp_1/artifacts/image-1/content?access=Zm9v";
  const media = [
    { kind: "image", url: `http://127.0.0.1:8324${file}`, name: "image-1" },
    {
      kind: "video",
      url: "/v1/tasks/video_1/artifacts/video/content?access=YmFy",
      name: "video",
      mime_type: "video/mp4",
    },
  ];
  expect(parseServiceTestResult({ ...result, media }).media).toEqual(media);
  for (const wrong of [
    [{ kind: "image", url: "https://images.example/cat.png", name: "cat" }],
    [{ kind: "image", url: "javascript:alert(1)", name: "x" }],
    [{ kind: "model", url: file, name: "x" }],
    [{ kind: "image", url: file }],
    "image",
  ]) {
    expect(() => parseServiceTestResult({ ...result, media: wrong })).toThrow(
      "Invalid provider test media",
    );
  }
});
