import { describe, expect, it } from "vitest";
import {
  adaptivePresets,
  adaptiveRange,
  defaultFailurePolicy,
  identityLearningKeys,
  identitySettingKeys,
  identityVersionKeys,
  maxSystemPromptLength,
  modelRedirectIssues,
  parseClientIdentities,
  parseFailurePolicy,
  parseFailoverPolicy,
  parseRoutingSettings,
  reasoningLevels,
  reasoningModes,
  subscriptionProtectionKeys,
  validIdentityVersion,
  type ModelRedirect,
} from "./failure-policy-model";

describe("failure policies", () => {
  it("defaults identity and subscription protections on for older settings and preserves explicit opt-out", () => {
    const settings = {
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: true,
      strategy: "failover_only",
      max_attempts: 6,
    };
    for (const key of [
      ...identitySettingKeys,
      ...subscriptionProtectionKeys,
      ...identityLearningKeys,
    ]) {
      expect(parseRoutingSettings(settings)[key]).toBe(true);
      expect(parseRoutingSettings({ ...settings, [key]: false })[key]).toBe(
        false,
      );
      for (const value of [null, "false", 0]) {
        expect(() =>
          parseRoutingSettings({ ...settings, [key]: value }),
        ).toThrow();
      }
    }
  });
  it("parses optional client version overrides with the core's rules", () => {
    const settings = {
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: true,
      strategy: "failover_only",
      max_attempts: 6,
    };
    const parsed = parseRoutingSettings(settings);
    for (const key of identityVersionKeys) expect(key in parsed).toBe(false);
    expect(
      parseRoutingSettings({
        ...settings,
        claude_identity_version: "2.1.300",
        codex_identity_version: "0.160.0",
      }),
    ).toMatchObject({
      claude_identity_version: "2.1.300",
      codex_identity_version: "0.160.0",
    });
    // A patch clears an override with an empty string.
    expect(
      "claude_identity_version" in
        parseRoutingSettings({ ...settings, claude_identity_version: "" }),
    ).toBe(false);
    for (const key of identityVersionKeys) {
      for (const version of [
        "2.1.300",
        "0.144.0",
        "2.2.0-beta.1",
        "1.0.0+build.5",
      ])
        expect(validIdentityVersion(key, version)).toBe(true);
      for (const version of [
        "2.1",
        "v2.1.300",
        "claude-cli/2.1.300",
        "2.1.300 (external, cli)",
        "2.1.300\n",
        "4294967296.0.0",
        `1.0.0-${"a".repeat(64)}`,
      ]) {
        expect(validIdentityVersion(key, version)).toBe(false);
        expect(() =>
          parseRoutingSettings({ ...settings, [key]: version }),
        ).toThrow();
      }
      for (const value of [null, 2, true])
        expect(() =>
          parseRoutingSettings({ ...settings, [key]: value }),
        ).toThrow();
    }
    // Codex rejects versions older than its backend accepts.
    for (const version of ["0.143.9", "0.144.0-alpha.1", "0.100.0"])
      expect(validIdentityVersion("codex_identity_version", version)).toBe(
        false,
      );
    for (const version of ["0.143.9", "0.144.0-alpha.1"])
      expect(validIdentityVersion("claude_identity_version", version)).toBe(
        true,
      );
    expect(
      validIdentityVersion("codex_identity_version", "0.144.1-alpha"),
    ).toBe(true);
  });
  it("parses the global prompt and thinking settings with defaults for absent keys", () => {
    const settings = {
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: true,
      strategy: "failover_only",
      max_attempts: 6,
    };
    expect(parseRoutingSettings(settings)).toMatchObject({
      system_prompt_enabled: false,
      system_prompt_prefix: "",
      system_prompt_suffix: "",
      reasoning_mode: "passthrough",
      reasoning_level: "medium",
      adaptive_preset: "everyday",
      adaptive_min: "low",
      adaptive_max: "max",
    });
    const longest = "\u{1F600}".repeat(maxSystemPromptLength);
    const saved = {
      system_prompt_enabled: true,
      system_prompt_prefix: "  Answer in English.\n\nBe brief.  ",
      system_prompt_suffix: longest,
    };
    expect(parseRoutingSettings({ ...settings, ...saved })).toMatchObject(
      saved,
    );
    for (const reasoning_mode of reasoningModes) {
      expect(
        parseRoutingSettings({ ...settings, reasoning_mode }),
      ).toMatchObject({ reasoning_mode, reasoning_level: "medium" });
      for (const reasoning_level of reasoningLevels) {
        const thinking = { reasoning_mode, reasoning_level };
        expect(
          parseRoutingSettings({ ...settings, ...thinking }),
        ).toMatchObject(thinking);
      }
    }
    for (const reasoning_level of reasoningLevels)
      expect(
        parseRoutingSettings({ ...settings, reasoning_level }),
      ).toMatchObject({ reasoning_mode: "passthrough", reasoning_level });
    for (const invalid of [
      { system_prompt_enabled: null },
      { system_prompt_enabled: "true" },
      { system_prompt_enabled: 1 },
      { system_prompt_prefix: null },
      { system_prompt_prefix: 1 },
      { system_prompt_suffix: ["text"] },
      { system_prompt_prefix: "x".repeat(maxSystemPromptLength + 1) },
      { system_prompt_suffix: `${longest}x` },
      { reasoning_mode: "" },
      { reasoning_mode: null },
      { reasoning_mode: "Fixed" },
      { reasoning_mode: "manual" },
      // The modes were renamed before release.
      { reasoning_mode: "follow" },
      { reasoning_mode: "auto" },
      { reasoning_mode: 1 },
      { reasoning_mode: ["adaptive"] },
      { reasoning_level: "" },
      { reasoning_level: null },
      { reasoning_level: "High" },
      { reasoning_level: "xhigh" },
      { reasoning_level: "minimal" },
      { reasoning_level: "none" },
      { reasoning_level: 3 },
      { reasoning_level: false },
      // The single override key was replaced by the mode and level.
      { reasoning_override: "" },
      { reasoning_override: "high" },
    ])
      expect(() => parseRoutingSettings({ ...settings, ...invalid })).toThrow();
  });
  it("parses the adaptive range and keeps its lower end at most its upper end", () => {
    const settings = {
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: true,
      strategy: "failover_only",
      max_attempts: 6,
    };
    for (const adaptive_preset of adaptivePresets)
      expect(
        parseRoutingSettings({ ...settings, adaptive_preset }),
      ).toMatchObject({
        adaptive_preset,
        adaptive_min: "low",
        adaptive_max: "max",
      });
    reasoningLevels.forEach((adaptive_min, index) => {
      for (const adaptive_max of reasoningLevels.slice(index)) {
        const range = { adaptive_min, adaptive_max };
        expect(parseRoutingSettings({ ...settings, ...range })).toMatchObject(
          range,
        );
      }
    });
    // An end the document lacks takes its default before the check.
    expect(
      parseRoutingSettings({ ...settings, adaptive_min: "max" }),
    ).toMatchObject({ adaptive_min: "max", adaptive_max: "max" });
    for (const invalid of [
      { adaptive_preset: "" },
      { adaptive_preset: null },
      { adaptive_preset: "daily" },
      { adaptive_preset: "Everyday" },
      { adaptive_min: "xhigh" },
      { adaptive_min: null },
      { adaptive_max: "none" },
      { adaptive_max: 4 },
      { adaptive_min: "low", adaptive_max: "off" },
      { adaptive_min: "max", adaptive_max: "high" },
      { adaptive_max: "off" },
      // The range is checked under every preset.
      {
        adaptive_preset: "research",
        adaptive_min: "high",
        adaptive_max: "low",
      },
    ])
      expect(() => parseRoutingSettings({ ...settings, ...invalid })).toThrow();
  });
  it("parses image compression with the core's rules and reads its absence as off", () => {
    const settings = {
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: true,
      strategy: "failover_only",
      max_attempts: 6,
    };
    expect(parseRoutingSettings(settings).image_compression).toEqual({
      enabled: false,
      mode: "lossless",
      quality: 85,
    });
    for (const image_compression of [
      { enabled: true, mode: "lossless", quality: 85 },
      { enabled: true, mode: "lossy", quality: 1 },
      { enabled: false, mode: "lossy", quality: 100 },
    ])
      expect(
        parseRoutingSettings({ ...settings, image_compression })
          .image_compression,
      ).toEqual(image_compression);
    for (const image_compression of [
      null,
      true,
      { enabled: true, mode: "lossless" },
      { enabled: "true", mode: "lossless", quality: 85 },
      { enabled: true, mode: "webp", quality: 85 },
      { enabled: true, mode: "lossy", quality: 0 },
      { enabled: true, mode: "lossy", quality: 101 },
      { enabled: true, mode: "lossy", quality: 80.5 },
      { enabled: true, mode: "lossy", quality: 80, max_side: 2048 },
    ])
      expect(() =>
        parseRoutingSettings({ ...settings, image_compression }),
      ).toThrow();
  });
  it("names the range each preset uses", () => {
    const custom = { adaptive_min: "off", adaptive_max: "high" } as const;
    expect(adaptiveRange({ adaptive_preset: "everyday", ...custom })).toEqual([
      "low",
      "max",
    ]);
    expect(adaptiveRange({ adaptive_preset: "research", ...custom })).toEqual([
      "medium",
      "max",
    ]);
    expect(adaptiveRange({ adaptive_preset: "custom", ...custom })).toEqual([
      "off",
      "high",
    ]);
    expect(adaptiveRange({})).toEqual(["low", "max"]);
  });
  it("parses learned and built-in client identity versions", () => {
    const identities = {
      codex: { learned_version: "0.160.0", builtin_version: "0.155.1" },
      claude: { builtin_version: "2.1.258" },
      grok: { builtin_version: "1.0.45", learned_version: "1.0.50" },
    };
    expect(parseClientIdentities(identities)).toEqual(identities);
    for (const invalid of [
      { ...identities, unknown: { builtin_version: "1.0.0" } },
      { codex: identities.codex },
      { ...identities, claude: { builtin_version: "2.1.258", source: "x" } },
      { ...identities, claude: {} },
      { ...identities, claude: { builtin_version: "" } },
      {
        ...identities,
        claude: { builtin_version: "2.1.258", learned_version: "" },
      },
      { ...identities, codex: { builtin_version: "v0.155.1" } },
      { ...identities, codex: { builtin_version: 1 } },
    ])
      expect(() => parseClientIdentities(invalid)).toThrow();
  });
  it("preserves the optional thinking signature recovery switch", () => {
    for (const enabled of [false, true]) {
      const policy = {
        ...defaultFailurePolicy(),
        thinking_signature_recovery: enabled,
      };
      expect(parseFailurePolicy(policy)).toEqual(policy);
    }
  });
  it("keeps the OpenAI repair switch independent from Claude repair", () => {
    for (const enabled of [false, true]) {
      const policy = {
        ...defaultFailurePolicy(),
        thinking_signature_recovery: false,
        openai_reasoning_recovery: enabled,
      };
      expect(parseFailurePolicy(policy)).toEqual(policy);
    }
  });
  it("keeps encrypted function-output repair opt-in and independent", () => {
    for (const enabled of [false, true]) {
      const policy = {
        ...defaultFailurePolicy(),
        openai_reasoning_recovery: false,
        openai_function_output_recovery: enabled,
      };
      expect(parseFailurePolicy(policy)).toEqual(policy);
    }
  });
  it("defines one global policy and treats complete exceptions as replacements", () => {
    const policy = defaultFailurePolicy();
    expect(parseFailurePolicy(policy)).toEqual(policy);
    expect(policy.max_retries).toBe(1);
    expect(policy.http_status["401"]).toBe("failover");
    expect(policy.http_status["529"]).toBe("retry_and_failover");
    expect(
      parseFailurePolicy({ ...policy, http_status: { "418": "retry" } })
        .http_status,
    ).toEqual({ "418": "retry" });
    expect(
      parseRoutingSettings({
        default_failure_policy: policy,
        allow_unmatched_failover: false,
        strategy: "retry_first",
        max_attempts: 6,
      }).default_failure_policy,
    ).toEqual(policy);
  });
  it("rejects malformed policies consistently at the desktop boundary", () => {
    for (const patch of [
      { max_retries: 6 },
      { max_retries: -1 },
      { max_retries: 1.5 },
      { max_delay_ms: 100 },
      { response_start_timeout_seconds: null },
      { thinking_signature_recovery: null },
      { thinking_signature_recovery: "true" },
      { openai_reasoning_recovery: null },
      { openai_reasoning_recovery: "true" },
      { openai_function_output_recovery: null },
      { openai_function_output_recovery: "true" },
      { network_error: "ignore" },
      { http_status: { "200": "retry" } },
      { http_status: { "429": "unknown" } },
    ]) {
      expect(() =>
        parseFailurePolicy({ ...defaultFailurePolicy(), ...patch }),
      ).toThrow();
    }
    expect(() => parseFailurePolicy({ max_retries: 1 })).toThrow();
    expect(() =>
      parseFailoverPolicy({
        enabled: true,
        strategy: "retry_first",
        max_attempts: 21,
      }),
    ).toThrow();
  });

  describe("model redirects", () => {
    const settings = {
      default_failure_policy: defaultFailurePolicy(),
      allow_unmatched_failover: true,
      strategy: "failover_only",
      max_attempts: 6,
    };
    const rule = (from: string, to: string, enabled = true) => ({
      from,
      to,
      enabled,
    });

    it("parses an absent list as empty and preserves saved rules in order", () => {
      expect(parseRoutingSettings(settings).model_redirects).toEqual([]);
      const model_redirects = [
        rule("gpt-4o", "gpt-5"),
        rule("astrlink/auto", "claude-sonnet-4-5", false),
      ];
      expect(
        parseRoutingSettings({ ...settings, model_redirects }).model_redirects,
      ).toEqual(model_redirects);
      expect(
        parseRoutingSettings({
          ...settings,
          model_redirects: Array.from({ length: 200 }, (_, index) =>
            rule(`model-${index}`, "gpt-5"),
          ),
        }).model_redirects,
      ).toHaveLength(200);
    });

    it("rejects malformed and invalid redirect lists", () => {
      for (const model_redirects of [
        null,
        {},
        [null],
        [{ from: "a", to: "b" }],
        [{ ...rule("a", "b"), extra: true }],
        [{ ...rule("a", "b"), enabled: "true" }],
        [{ ...rule("a", "b"), from: 1 }],
        [rule("a", "a")],
        [rule("a", "astrlink/auto")],
        [rule("a", "b"), rule("a", "c", false)],
        [rule("a", "b"), rule("b", "c")],
        Array.from({ length: 201 }, (_, index) =>
          rule(`model-${index}`, "gpt-5"),
        ),
      ]) {
        expect(() =>
          parseRoutingSettings({ ...settings, model_redirects }),
        ).toThrow();
      }
    });

    it("mirrors the gateway validation rules with the first issue per row", () => {
      const cases: [ModelRedirect[], ReturnType<typeof modelRedirectIssues>][] =
        [
          [[rule("", "gpt-5")], ["empty_from"]],
          [[rule("gpt-4o", "")], ["empty_to"]],
          [[rule("", "")], ["empty_from"]],
          [[rule("x".repeat(257), "gpt-5")], ["too_long"]],
          [[rule("\u{1F600}".repeat(256), "gpt-5")], [undefined]],
          [[rule("gpt-4o", "gpt\n5")], ["control_character"]],
          [[rule("gpt\u00004o", "gpt-5")], ["control_character"]],
          [[rule("gpt\t4o", "gpt-5")], [undefined]],
          [[rule(" gpt-4o", "gpt-5")], ["whitespace"]],
          [[rule("gpt-4o", "gpt-5\u0085")], ["whitespace"]],
          [[rule("gpt-4o", "\u3000gpt-5")], ["whitespace"]],
          // Go's TrimSpace keeps U+FEFF, unlike String.prototype.trim.
          [[rule("\ufeffgpt-4o", "gpt-5")], [undefined]],
          [[rule("gpt-5", "gpt-5")], ["same_model"]],
          [[rule("GPT-5", "gpt-5")], [undefined]],
          [[rule("gpt-4o", "astrlink/auto")], ["auto_target"]],
          [[rule("astrlink/auto", "gpt-5")], [undefined]],
          [
            [rule("gpt-4o", "gpt-5"), rule("gpt-4o", "gpt-5-mini", false)],
            ["duplicate_from", "duplicate_from"],
          ],
          [
            [rule("gpt-4o", "gpt-5"), rule("gpt-4", "gpt-4o", false)],
            [undefined, "chained_target"],
          ],
        ];
      for (const [redirects, issues] of cases)
        expect(modelRedirectIssues(redirects)).toEqual(issues);
    });
  });
});
