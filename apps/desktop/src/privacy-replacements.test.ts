import { describe, expect, it } from "vitest";

import {
  jsonPointerLabel,
  privacyFindingSites,
  privacyReplacements,
  splitCarriedReplacements,
} from "./privacy-replacements";

const email = "<PRIVATE_EMAIL_0123456789abcdef>";
const person = "<PRIVATE_PERSON_fedcba9876543210>";

describe("privacyReplacements", () => {
  it("lists each stand-in once, with where it sits and the text around it", () => {
    const body = JSON.stringify({
      instructions:
        "Treat redaction markers of the form <PRIVATE_KIND_hex> or <SECRET_hex> as opaque.",
      input: [
        {
          role: "user",
          content: [
            { type: "input_text", text: `Ask ${person} to email ${email}.` },
          ],
        },
        { type: "function_call_output", output: `sent to ${email}` },
        {
          role: "assistant",
          content: "Reach redacted-0123456789ab@private.invalid instead.",
        },
      ],
    });

    const replacements = privacyReplacements(body);

    expect(
      replacements.map(({ placeholder, kind, label, count }) => ({
        placeholder,
        kind,
        label,
        count,
      })),
    ).toEqual([
      {
        placeholder: person,
        kind: "private_person",
        label: "人名",
        count: 1,
      },
      { placeholder: email, kind: "email", label: "邮箱", count: 2 },
      {
        placeholder: "redacted-0123456789ab@private.invalid",
        kind: "email",
        label: "邮箱",
        count: 1,
      },
    ]);
    expect(replacements[1].sites).toEqual([
      {
        path: "input[0].content[0].text",
        role: "user",
        before: `Ask ${person} to email `,
        after: ".",
      },
      {
        path: "input[1].output",
        role: null,
        before: "sent to ",
        after: "",
      },
    ]);
    expect(replacements[2].sites[0]).toMatchObject({
      path: "input[2].content",
      role: "assistant",
    });
  });

  it("trims long context and keeps only the first few places", () => {
    const filler = "word ".repeat(40);
    const body = JSON.stringify({
      messages: Array.from({ length: 5 }, () => ({
        role: "user",
        content: `${filler}\n\n${email}\n${filler}`,
      })),
    });

    const [replacement] = privacyReplacements(body);

    expect(replacement.count).toBe(5);
    expect(replacement.sites).toHaveLength(3);
    const [site] = replacement.sites;
    expect(site.before.startsWith("…")).toBe(true);
    expect(site.before.endsWith("word ")).toBe(true);
    expect(site.before.length).toBeLessThanOrEqual(49);
    expect(site.after.startsWith(" word")).toBe(true);
    expect(site.after.endsWith("…")).toBe(true);
    expect(site.before).not.toContain("\n");
  });

  it("stops the context short of a neighbouring marker", () => {
    const padding = "x".repeat(30);
    const body = JSON.stringify({
      input: `${person}${padding} ${email} ${padding}${person}`,
    });

    const site = privacyReplacements(body).find(
      (replacement) => replacement.placeholder === email,
    )?.sites[0];

    expect(site?.before).toBe(`…${padding} `);
    expect(site?.after).toBe(` ${padding}…`);

    const natural = "redacted-0123456789ab@private.invalid";
    const [first] = privacyReplacements(
      JSON.stringify({ input: `${person} ${padding} ${natural}` }),
    );
    expect(first.sites[0].after).toBe(` ${padding}…`);
  });

  it("leaves out natural stand-ins of kinds the call did not replace", () => {
    const body = JSON.stringify({
      input: [
        {
          role: "user",
          content: `Mail ${email} or redacted-0123456789ab@private.invalid`,
        },
        { type: "function_call_output", output: "Ranges: `192.0.2.0/24`" },
      ],
    });

    expect(
      privacyReplacements(body, new Set(["email"])).map(
        (replacement) => replacement.placeholder,
      ),
    ).toEqual([email, "redacted-0123456789ab@private.invalid"]);
    // Minted markers are the gateway's own, whatever the recorded kinds.
    expect(
      privacyReplacements(body, new Set(["ip_address"])).map(
        (replacement) => replacement.placeholder,
      ),
    ).toEqual([email, "192.0.2.0"]);
  });

  it("scans a body that is not JSON as plain text", () => {
    expect(privacyReplacements(`partial {"input": "${email}`)).toMatchObject([
      { placeholder: email, count: 1, sites: [{ path: null, role: null }] },
    ]);
  });

  it("finds nothing in a body without stand-ins", () => {
    expect(privacyReplacements('{"input":"hello"}')).toEqual([]);
  });
});

describe("splitCarriedReplacements", () => {
  const current = "<SECRET_d3a84802e57a3f0e>";
  const first = "<SECRET_2ec13e4c43f265d0>";
  const second = "<SECRET_60fffaf802a51666>";
  const body = JSON.stringify({
    input: [
      {
        role: "user",
        content: [{ type: "input_text", text: `${current} test the relay` }],
      },
      {
        type: "reasoning",
        content: [
          { type: "reasoning_text", text: `The message holds ${first}.` },
        ],
      },
      {
        type: "function_call",
        arguments: `{"command":"curl -H 'Authorization: Bearer ${current}'"}`,
      },
      {
        role: "assistant",
        content: [{ type: "output_text", text: `Bearer ${second}` }],
      },
    ],
  });
  const secretAt = (...paths: string[]) =>
    paths.map((json_path) => ({ kind: "common_secret", json_path, count: 1 }));
  const placeholders = ({
    made,
    carried,
  }: ReturnType<typeof splitCarriedReplacements>) => ({
    made: made.map((replacement) => replacement.placeholder),
    carried: carried.map((replacement) => replacement.placeholder),
  });

  it("separates the markers a call made from older ones it carried", () => {
    expect(
      placeholders(
        splitCarriedReplacements(
          privacyReplacements(body),
          secretAt("/input/0/content/0/text", "/input/2/arguments"),
        ),
      ),
    ).toEqual({ made: [current], carried: [first, second] });
  });

  it("counts a marker of a kind the call never found as carried", () => {
    expect(
      placeholders(
        splitCarriedReplacements(privacyReplacements(body), [
          { kind: "email", json_path: "/input/0/content/0/text", count: 1 },
        ]),
      ),
    ).toEqual({ made: [], carried: [current, first, second] });
  });

  it("keeps every marker made when the findings do not line up", () => {
    const replacements = privacyReplacements(body);
    expect(
      placeholders(splitCarriedReplacements(replacements, undefined)),
    ).toEqual({ made: [current, first, second], carried: [] });
    // A notice inserted ahead of the messages shifts every index.
    expect(
      placeholders(
        splitCarriedReplacements(replacements, secretAt("/input/5/content")),
      ),
    ).toEqual({ made: [current, first, second], carried: [] });
  });

  it("drops a natural stand-in away from every finding", () => {
    const ips = JSON.stringify({
      input: [
        { role: "user", content: "ping 192.0.2.7" },
        { type: "function_call_output", output: "Ranges: `192.0.2.0/24`" },
      ],
    });
    expect(
      placeholders(
        splitCarriedReplacements(privacyReplacements(ips), [
          { kind: "ip_address", json_path: "/input/0/content", count: 1 },
        ]),
      ),
    ).toEqual({ made: ["192.0.2.7"], carried: [] });
  });
});

describe("jsonPointerLabel", () => {
  it("writes a JSON pointer the way replacement sites are written", () => {
    expect(jsonPointerLabel("/input/0/content")).toBe("input[0].content");
    expect(jsonPointerLabel("/messages/2/content/1/text")).toBe(
      "messages[2].content[1].text",
    );
    expect(jsonPointerLabel("/a~1b/c~0d")).toBe("a/b.c~d");
    expect(jsonPointerLabel("")).toBe("");
  });
});

describe("privacyFindingSites", () => {
  it("groups findings by location in the request", () => {
    expect(
      privacyFindingSites([
        { kind: "email", json_path: "/input/0/content", count: 1 },
        { kind: "common_secret", json_path: "/input/0/content", count: 1 },
        { kind: "email", json_path: "/input/0/content", count: 1 },
        { kind: "email", json_path: "/input/2/content", count: 1 },
      ]),
    ).toEqual([
      {
        path: "input[0].content",
        kinds: [
          { kind: "email", label: "邮箱", count: 2 },
          { kind: "common_secret", label: "常见密钥", count: 1 },
        ],
      },
      {
        path: "input[2].content",
        kinds: [{ kind: "email", label: "邮箱", count: 1 }],
      },
    ]);
    expect(privacyFindingSites(undefined)).toEqual([]);
  });
});
