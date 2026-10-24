// The web APIs AstrLink adds to every plugin runtime, on top of what moejs
// has (atob, btoa, queueMicrotask, TextEncoder, TextDecoder,
// structuredClone): URL, URLSearchParams, Headers, Response, fetch,
// AbortController, console, crypto and performance. The timers come from the
// host. host is the host's primitives; nothing else reaches them.
"use strict";
(function (host) {
  const define = (name, value) =>
    Object.defineProperty(globalThis, name, {
      value,
      writable: true,
      configurable: true,
      enumerable: false,
    });
  const encoder = new TextEncoder();
  const decoder = new TextDecoder();

  const toBytes = (data, what) => {
    if (data instanceof ArrayBuffer) return new Uint8Array(data.slice(0));
    if (ArrayBuffer.isView(data))
      return new Uint8Array(
        data.buffer.slice(data.byteOffset, data.byteOffset + data.byteLength),
      );
    if (typeof data === "string") return encoder.encode(data);
    throw new TypeError(
      `${what} must be a string, an ArrayBuffer or a typed array`,
    );
  };

  // percentDecode turns %XX sequences into the bytes they stand for and
  // reads the result as UTF-8; a malformed sequence stays as it is.
  const percentDecode = (text) => {
    if (!text.includes("%")) return text;
    const bytes = [];
    for (let index = 0; index < text.length; index++) {
      const char = text[index];
      if (
        char === "%" &&
        /^[0-9A-Fa-f]{2}$/.test(text.slice(index + 1, index + 3))
      ) {
        bytes.push(parseInt(text.slice(index + 1, index + 3), 16));
        index += 2;
        continue;
      }
      const code = text.codePointAt(index);
      const unit = String.fromCodePoint(code);
      for (const byte of encoder.encode(unit)) bytes.push(byte);
      index += unit.length - 1;
    }
    return decoder.decode(new Uint8Array(bytes));
  };
  const formEncode = (text) =>
    encodeURIComponent(text)
      .replace(
        /[!'()~]/g,
        (char) => "%" + char.charCodeAt(0).toString(16).toUpperCase(),
      )
      .replace(/%20/g, "+");
  const formDecode = (text) => percentDecode(text.replace(/\+/g, " "));

  const searchOwner = Symbol("url");
  const resetSearch = Symbol("resetSearch");
  const updateSearch = Symbol("updateSearch");

  class URLSearchParams {
    #list = [];
    constructor(init = "") {
      if (init instanceof URLSearchParams) {
        this.#list = init.#list.map(([name, value]) => [name, value]);
      } else if (init !== null && typeof init === "object") {
        if (typeof init[Symbol.iterator] === "function") {
          for (const pair of init) {
            const entry = [...pair];
            if (entry.length !== 2)
              throw new TypeError(
                "each pair of URLSearchParams must have two items",
              );
            this.#list.push([String(entry[0]), String(entry[1])]);
          }
        } else {
          for (const name of Object.keys(init))
            this.#list.push([name, String(init[name])]);
        }
      } else {
        this.#parse(String(init));
      }
    }
    #parse(text) {
      this.#list = [];
      if (text.startsWith("?")) text = text.slice(1);
      for (const part of text.split("&")) {
        if (part === "") continue;
        const equals = part.indexOf("=");
        const name = equals < 0 ? part : part.slice(0, equals);
        const value = equals < 0 ? "" : part.slice(equals + 1);
        this.#list.push([formDecode(name), formDecode(value)]);
      }
    }
    [resetSearch](text) {
      this.#parse(text);
    }
    #changed() {
      const owner = this[searchOwner];
      if (owner) owner[updateSearch](this.toString());
    }
    get size() {
      return this.#list.length;
    }
    append(name, value) {
      this.#list.push([String(name), String(value)]);
      this.#changed();
    }
    delete(name, value) {
      name = String(name);
      this.#list = this.#list.filter(
        ([n, v]) => n !== name || (value !== undefined && v !== String(value)),
      );
      this.#changed();
    }
    get(name) {
      name = String(name);
      const found = this.#list.find(([n]) => n === name);
      return found ? found[1] : null;
    }
    getAll(name) {
      name = String(name);
      return this.#list.filter(([n]) => n === name).map(([, v]) => v);
    }
    has(name, value) {
      name = String(name);
      return this.#list.some(
        ([n, v]) => n === name && (value === undefined || v === String(value)),
      );
    }
    set(name, value) {
      name = String(name);
      value = String(value);
      const index = this.#list.findIndex(([n]) => n === name);
      if (index < 0) {
        this.#list.push([name, value]);
      } else {
        this.#list[index] = [name, value];
        this.#list = this.#list.filter(([n], at) => at <= index || n !== name);
      }
      this.#changed();
    }
    sort() {
      this.#list = this.#list
        .map((entry, index) => [entry, index])
        .sort(([a, i], [b, j]) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : i - j))
        .map(([entry]) => entry);
      this.#changed();
    }
    forEach(callback, thisArg) {
      for (const [name, value] of this.#list)
        callback.call(thisArg, value, name, this);
    }
    *entries() {
      for (const [name, value] of this.#list) yield [name, value];
    }
    *keys() {
      for (const [name] of this.#list) yield name;
    }
    *values() {
      for (const [, value] of this.#list) yield value;
    }
    [Symbol.iterator]() {
      return this.entries();
    }
    toString() {
      return this.#list
        .map(([name, value]) => formEncode(name) + "=" + formEncode(value))
        .join("&");
    }
    get [Symbol.toStringTag]() {
      return "URLSearchParams";
    }
  }

  class URL {
    #parts;
    #params = null;
    constructor(url, base) {
      const parts = host.parseURL(
        String(url),
        base === undefined ? undefined : String(base),
      );
      if (!parts) throw new TypeError(`Invalid URL: ${String(url)}`);
      this.#parts = parts;
    }
    static canParse(url, base) {
      return (
        host.parseURL(
          String(url),
          base === undefined ? undefined : String(base),
        ) !== null
      );
    }
    static parse(url, base) {
      try {
        return new URL(url, base);
      } catch {
        return null;
      }
    }
    #reparse(href, fromParams = false) {
      const parts = host.parseURL(href);
      if (parts) {
        this.#parts = parts;
        if (this.#params && !fromParams)
          this.#params[resetSearch](parts.search);
      }
    }
    [updateSearch](text) {
      this.#reparse(
        this.#with({ search: text === "" ? "" : "?" + text }),
        true,
      );
    }
    #with(changes) {
      const parts = { ...this.#parts, ...changes };
      return URL.#serialize(parts);
    }
    static #serialize(parts) {
      if (parts.opaque)
        return parts.protocol + parts.pathname + parts.search + parts.hash;
      let authority = "";
      if (parts.username !== "" || parts.password !== "") {
        authority =
          parts.username +
          (parts.password !== "" ? ":" + parts.password : "") +
          "@";
      }
      const hostPart =
        parts.hostname + (parts.port !== "" ? ":" + parts.port : "");
      return (
        parts.protocol +
        "//" +
        authority +
        hostPart +
        parts.pathname +
        parts.search +
        parts.hash
      );
    }
    get href() {
      return URL.#serialize(this.#parts);
    }
    set href(value) {
      const parts = host.parseURL(String(value));
      if (!parts) throw new TypeError(`Invalid URL: ${String(value)}`);
      this.#reparse(String(value));
    }
    get origin() {
      const { protocol } = this.#parts;
      if (
        protocol === "http:" ||
        protocol === "https:" ||
        protocol === "ws:" ||
        protocol === "wss:" ||
        protocol === "ftp:"
      ) {
        return protocol + "//" + this.host;
      }
      return "null";
    }
    get protocol() {
      return this.#parts.protocol;
    }
    set protocol(value) {
      this.#reparse(
        this.#with({ protocol: String(value).replace(/:?$/, ":") }),
      );
    }
    get username() {
      return this.#parts.username;
    }
    set username(value) {
      this.#reparse(
        this.#with({ username: encodeURIComponent(String(value)) }),
      );
    }
    get password() {
      return this.#parts.password;
    }
    set password(value) {
      this.#reparse(
        this.#with({ password: encodeURIComponent(String(value)) }),
      );
    }
    get host() {
      return (
        this.#parts.hostname +
        (this.#parts.port !== "" ? ":" + this.#parts.port : "")
      );
    }
    set host(value) {
      const text = String(value);
      const colon = text.lastIndexOf(":");
      if (colon > text.lastIndexOf("]")) {
        this.#reparse(
          this.#with({
            hostname: text.slice(0, colon),
            port: text.slice(colon + 1),
          }),
        );
      } else {
        this.#reparse(this.#with({ hostname: text }));
      }
    }
    get hostname() {
      return this.#parts.hostname;
    }
    set hostname(value) {
      this.#reparse(this.#with({ hostname: String(value) }));
    }
    get port() {
      return this.#parts.port;
    }
    set port(value) {
      this.#reparse(this.#with({ port: String(value) }));
    }
    get pathname() {
      return this.#parts.pathname;
    }
    set pathname(value) {
      let path = String(value);
      if (!this.#parts.opaque && !path.startsWith("/")) path = "/" + path;
      this.#reparse(this.#with({ pathname: path }));
    }
    get search() {
      return this.#parts.search;
    }
    set search(value) {
      let text = String(value);
      if (text !== "" && !text.startsWith("?")) text = "?" + text;
      if (text === "?") text = "";
      this.#reparse(this.#with({ search: text }));
    }
    get searchParams() {
      if (!this.#params) {
        const params = new URLSearchParams(this.#parts.search);
        params[searchOwner] = this;
        this.#params = params;
      }
      return this.#params;
    }
    get hash() {
      return this.#parts.hash;
    }
    set hash(value) {
      let text = String(value);
      if (text !== "" && !text.startsWith("#")) text = "#" + text;
      if (text === "#") text = "";
      this.#reparse(this.#with({ hash: text }));
    }
    toString() {
      return this.href;
    }
    toJSON() {
      return this.href;
    }
    get [Symbol.toStringTag]() {
      return "URL";
    }
  }

  const tokenPattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
  const headerName = (name) => {
    const text = String(name);
    if (!tokenPattern.test(text))
      throw new TypeError(`"${text}" is not a valid header name`);
    return text.toLowerCase();
  };
  const headerValue = (value) => {
    const text = String(value).replace(/^[\t\n\r ]+|[\t\n\r ]+$/g, "");
    if (text.includes("\r") || text.includes("\n") || text.includes("\u0000")) {
      throw new TypeError("a header value cannot hold line breaks or NUL");
    }
    return text;
  };

  class Headers {
    #map = new Map();
    constructor(init) {
      if (init === undefined || init === null) return;
      if (init instanceof Headers) {
        init.forEach((value, name) => this.append(name, value));
      } else if (typeof init[Symbol.iterator] === "function") {
        for (const pair of init) {
          const entry = [...pair];
          if (entry.length !== 2)
            throw new TypeError("each header pair must have two items");
          this.append(entry[0], entry[1]);
        }
      } else if (typeof init === "object") {
        for (const name of Object.keys(init)) this.append(name, init[name]);
      } else {
        throw new TypeError(
          "Headers takes an object, an array of pairs or Headers",
        );
      }
    }
    append(name, value) {
      const key = headerName(name);
      const list = this.#map.get(key);
      if (list) list.push(headerValue(value));
      else this.#map.set(key, [headerValue(value)]);
    }
    set(name, value) {
      this.#map.set(headerName(name), [headerValue(value)]);
    }
    get(name) {
      const list = this.#map.get(headerName(name));
      return list ? list.join(", ") : null;
    }
    getSetCookie() {
      return [...(this.#map.get("set-cookie") ?? [])];
    }
    has(name) {
      return this.#map.has(headerName(name));
    }
    delete(name) {
      this.#map.delete(headerName(name));
    }
    forEach(callback, thisArg) {
      for (const [name, value] of this.entries())
        callback.call(thisArg, value, name, this);
    }
    *entries() {
      for (const name of [...this.#map.keys()].sort())
        yield [name, this.#map.get(name).join(", ")];
    }
    *keys() {
      for (const [name] of this.entries()) yield name;
    }
    *values() {
      for (const [, value] of this.entries()) yield value;
    }
    [Symbol.iterator]() {
      return this.entries();
    }
    get [Symbol.toStringTag]() {
      return "Headers";
    }
  }

  const bodyBytes = (body) => {
    if (body === undefined || body === null) return null;
    if (body instanceof URLSearchParams) return encoder.encode(body.toString());
    return toBytes(body, "the body");
  };

  class Response {
    #body;
    #used = false;
    constructor(body = null, init = {}) {
      this.#body = bodyBytes(body);
      this.status = init.status ?? 200;
      this.statusText = init.statusText ?? "";
      this.headers = new Headers(init.headers);
      this.url = "";
      this.redirected = false;
      this.type = "default";
      if (typeof body === "string" && !this.headers.has("content-type")) {
        this.headers.set("content-type", "text/plain;charset=UTF-8");
      }
    }
    get ok() {
      return this.status >= 200 && this.status <= 299;
    }
    get bodyUsed() {
      return this.#used;
    }
    #consume() {
      if (this.#used)
        return Promise.reject(new TypeError("the body has already been read"));
      this.#used = true;
      return Promise.resolve(this.#body ?? new Uint8Array(0));
    }
    arrayBuffer() {
      return this.#consume().then((bytes) => bytes.slice().buffer);
    }
    bytes() {
      return this.#consume().then((bytes) => bytes.slice());
    }
    text() {
      return this.#consume().then((bytes) => decoder.decode(bytes));
    }
    json() {
      return this.text().then((text) => JSON.parse(text));
    }
    clone() {
      if (this.#used) throw new TypeError("the body has already been read");
      const copy = new Response(this.#body, {
        status: this.status,
        statusText: this.statusText,
        headers: this.headers,
      });
      copy.url = this.url;
      return copy;
    }
    static json(data, init = {}) {
      const response = new Response(JSON.stringify(data), init);
      response.headers.set("content-type", "application/json");
      return response;
    }
    static error() {
      const response = new Response(null, { status: 0 });
      response.type = "error";
      return response;
    }
    get [Symbol.toStringTag]() {
      return "Response";
    }
  }

  class AbortSignal {
    #aborted = false;
    #reason = undefined;
    #listeners = [];
    onabort = null;
    get aborted() {
      return this.#aborted;
    }
    get reason() {
      return this.#reason;
    }
    throwIfAborted() {
      if (this.#aborted) throw this.#reason;
    }
    addEventListener(type, listener) {
      if (type === "abort" && typeof listener === "function")
        this.#listeners.push(listener);
    }
    removeEventListener(type, listener) {
      this.#listeners = this.#listeners.filter((each) => each !== listener);
    }
    static abort(reason) {
      const controller = new AbortController();
      controller.abort(reason);
      return controller.signal;
    }
    static timeout(milliseconds) {
      const controller = new AbortController();
      setTimeout(
        () =>
          controller.abort(
            abortError("TimeoutError", "the operation timed out"),
          ),
        milliseconds,
      );
      return controller.signal;
    }
    static _abort(signal, reason) {
      if (signal.#aborted) return;
      signal.#aborted = true;
      signal.#reason =
        reason === undefined
          ? abortError("AbortError", "the operation was aborted")
          : reason;
      const event = { type: "abort", target: signal };
      if (typeof signal.onabort === "function") signal.onabort(event);
      for (const listener of signal.#listeners) listener(event);
    }
  }
  const abortError = (name, message) => {
    const error = new Error(message);
    error.name = name;
    return error;
  };
  const abortSignal = AbortSignal._abort;
  delete AbortSignal._abort;

  class AbortController {
    #signal = new AbortSignal();
    get signal() {
      return this.#signal;
    }
    abort(reason) {
      abortSignal(this.#signal, reason);
    }
  }

  const fetchURL = (input) => {
    if (input instanceof URL) return input.href;
    if (input !== null && typeof input === "object" && "url" in input)
      return String(input.url);
    return String(input);
  };

  async function fetch(input, init = {}) {
    const url = fetchURL(input);
    const method = String(init.method ?? "GET").toUpperCase();
    const headers = new Headers(init.headers);
    const request = { method, url, headers: {} };
    const body = init.body;
    if (body !== undefined && body !== null) {
      if (typeof body === "string") {
        request.body = body;
        if (!headers.has("content-type"))
          headers.set("content-type", "text/plain;charset=UTF-8");
      } else if (body instanceof URLSearchParams) {
        request.body = body.toString();
        if (!headers.has("content-type"))
          headers.set(
            "content-type",
            "application/x-www-form-urlencoded;charset=UTF-8",
          );
      } else {
        request.bodyBase64 = toBytes(body, "the fetch body").toBase64();
      }
    }
    headers.forEach((value, name) => {
      request.headers[name] = value;
    });
    if (init.credential !== undefined)
      request.credential = String(init.credential);
    if (init.timeoutMs !== undefined)
      request.timeoutMs = Number(init.timeoutMs);
    const signal = init.signal;
    if (signal?.aborted) throw signal.reason;
    let pending = host.fetch(request);
    if (signal) {
      const fetched = pending;
      pending = new Promise((resolve, reject) => {
        signal.addEventListener("abort", () => reject(signal.reason));
        fetched.then(resolve, reject);
      });
    }
    const raw = await pending;
    if (raw.error) {
      const error = new TypeError(`fetch failed: ${raw.error}`);
      error.cause = { code: raw.error, status: raw.status };
      throw error;
    }
    const response = new Response(
      raw.body ? Uint8Array.fromBase64(raw.body) : null,
      {
        status: raw.status,
        statusText: raw.statusText ?? "",
        headers: raw.headers,
      },
    );
    response.url = url;
    return response;
  }

  const formatValue = (value) => {
    switch (typeof value) {
      case "string":
        return value;
      case "undefined":
        return "undefined";
      case "function":
        return `[Function ${value.name || "anonymous"}]`;
      case "symbol":
        return value.toString();
      case "bigint":
        return `${value}n`;
      case "object":
        if (value === null) return "null";
        if (value instanceof Error)
          return value.stack || `${value.name}: ${value.message}`;
        try {
          const text = JSON.stringify(value, (_, item) =>
            typeof item === "bigint" ? `${item}n` : item,
          );
          return text === undefined ? String(value) : text;
        } catch {
          return String(value);
        }
      default:
        return String(value);
    }
  };
  const writer =
    (level) =>
    (...args) =>
      host.log(level, args.map(formatValue).join(" "));
  const console = {
    log: writer("info"),
    info: writer("info"),
    debug: writer("debug"),
    trace: writer("debug"),
    warn: writer("warn"),
    error: writer("warn"),
  };

  const hashName = (algorithm) => {
    const name = typeof algorithm === "string" ? algorithm : algorithm?.name;
    return String(name).toUpperCase();
  };
  const keys = new WeakMap();
  class CryptoKey {
    constructor(algorithm, extractable, usages) {
      this.type = "secret";
      this.algorithm = algorithm;
      this.extractable = extractable;
      this.usages = usages;
    }
    get [Symbol.toStringTag]() {
      return "CryptoKey";
    }
  }
  const hmacKey = (key) => {
    const bytes = keys.get(key);
    if (!bytes)
      throw new TypeError(
        "the key is not an HMAC key of crypto.subtle.importKey",
      );
    return bytes;
  };
  const subtle = {
    async digest(algorithm, data) {
      return Uint8Array.fromBase64(
        host.digest(hashName(algorithm), toBytes(data, "the data").toBase64()),
      ).buffer;
    },
    async importKey(format, keyData, algorithm, extractable, usages) {
      if (format !== "raw")
        throw new TypeError("crypto.subtle.importKey supports the raw format");
      if (hashName(algorithm) !== "HMAC")
        throw new TypeError("crypto.subtle.importKey supports HMAC keys");
      const hash = hashName(algorithm.hash);
      const key = new CryptoKey(
        { name: "HMAC", hash: { name: hash } },
        Boolean(extractable),
        [...usages],
      );
      keys.set(key, toBytes(keyData, "the key"));
      return key;
    },
    async sign(algorithm, key, data) {
      if (hashName(algorithm) !== "HMAC")
        throw new TypeError("crypto.subtle.sign supports HMAC");
      const signature = host.hmac(
        key.algorithm.hash.name,
        hmacKey(key).toBase64(),
        toBytes(data, "the data").toBase64(),
      );
      return Uint8Array.fromBase64(signature).buffer;
    },
    async verify(algorithm, key, signature, data) {
      const expected = new Uint8Array(await subtle.sign(algorithm, key, data));
      const given = toBytes(signature, "the signature");
      if (expected.length !== given.length) return false;
      let difference = 0;
      for (let index = 0; index < expected.length; index++)
        difference |= expected[index] ^ given[index];
      return difference === 0;
    },
  };
  const crypto = {
    getRandomValues(array) {
      if (
        !ArrayBuffer.isView(array) ||
        array instanceof Float32Array ||
        array instanceof Float64Array ||
        array instanceof DataView
      ) {
        throw new TypeError(
          "crypto.getRandomValues takes an integer typed array",
        );
      }
      if (array.byteLength > 65536)
        throw new RangeError(
          "crypto.getRandomValues fills at most 65536 bytes",
        );
      new Uint8Array(array.buffer, array.byteOffset, array.byteLength).set(
        Uint8Array.fromBase64(host.randomBytes(array.byteLength)),
      );
      return array;
    },
    randomUUID() {
      return host.randomUUID();
    },
    subtle,
  };

  // ---- astrlink.textStream: the reply's text across protocols ----

  // escapePattern turns a literal into a pattern that matches it.
  const escapePattern = (text) => text.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

  // replacer builds the streaming replacement of options: the pattern of
  // every replacement, how to replace a match, and how much text a match may
  // still need from what has not come yet.
  const replacer = (options) => {
    const rules = (options.replace ?? []).map(([from, to]) => {
      if (typeof from === "string") {
        if (from === "")
          throw new TypeError(
            "textStream: a replacement needs text to replace",
          );
        return {
          source: escapePattern(from),
          flags: "",
          to,
          longest: from.length,
        };
      }
      if (from instanceof RegExp) {
        return {
          source: from.source,
          flags: from.flags,
          to,
          longest: options.maxMatch ?? 64,
        };
      }
      throw new TypeError(
        "textStream: replace takes [string or RegExp, replacement] pairs",
      );
    });
    if (rules.length === 0) return null;
    const flags = new Set(["g"]);
    for (const rule of rules)
      for (const flag of rule.flags)
        if ("imsuv".includes(flag)) flags.add(flag);
    const pattern = new RegExp(
      rules.map((rule) => `(${rule.source})`).join("|"),
      [...flags].join(""),
    );
    // Each rule's groups start after those of the rules before it.
    let group = 1;
    for (const rule of rules) {
      rule.group = group;
      group += 1 + new RegExp(`${rule.source}|`).exec("").length - 1;
    }
    const hold = Math.max(0, ...rules.map((rule) => rule.longest - 1));
    const replace = (match) => {
      const rule = rules.find(
        (candidate) => match[candidate.group] !== undefined,
      );
      if (!rule) return match[0];
      return typeof rule.to === "function"
        ? String(rule.to(match[rule.group]))
        : String(rule.to);
    };
    return { pattern, hold, replace };
  };

  // flush replaces what text can be replaced for sure and returns it with
  // the rest, which a match may still run into; final takes everything.
  const flush = (rules, text, final) => {
    if (!rules) return [text, ""];
    const limit = final ? text.length : text.length - rules.hold;
    if (limit <= 0) return ["", text];
    let out = "";
    let position = 0;
    rules.pattern.lastIndex = 0;
    let match;
    while ((match = rules.pattern.exec(text)) !== null) {
      if (match[0] === "") {
        rules.pattern.lastIndex++;
        continue;
      }
      const end = match.index + match[0].length;
      if (match.index >= limit || (!final && end === text.length)) break;
      out += text.slice(position, match.index) + rules.replace(match);
      position = end;
    }
    const cut = Math.max(limit, position);
    if (position < cut) out += text.slice(position, cut);
    return [out, text.slice(cut)];
  };

  // The text of each protocol's stream events: where a piece of the reply
  // is, and the events that end one.
  const streams = {
    "openai.chat": (event) => {
      const choice = event.data?.choices?.[0];
      if (!choice || typeof choice !== "object") return null;
      const content = choice.delta?.content;
      return {
        key: "c" + (choice.index ?? 0),
        text: typeof content === "string" ? content : "",
        final: choice.finish_reason != null,
        write: (text) => {
          const data = structuredClone(event.data);
          data.choices[0].delta = { ...data.choices[0].delta, content: text };
          return [{ ...event, data }];
        },
      };
    },
    "anthropic.messages": (event) => {
      const data = event.data;
      if (
        data?.type === "content_block_delta" &&
        data.delta?.type === "text_delta"
      ) {
        return {
          key: "b" + data.index,
          text: data.delta.text ?? "",
          write: (text) => [
            { ...event, data: { ...data, delta: { ...data.delta, text } } },
          ],
        };
      }
      if (data?.type === "content_block_stop") {
        return {
          key: "b" + data.index,
          text: "",
          final: true,
          write: (text) => [
            ...(text
              ? [
                  {
                    event: "content_block_delta",
                    data: {
                      type: "content_block_delta",
                      index: data.index,
                      delta: { type: "text_delta", text },
                    },
                  },
                ]
              : []),
            event,
          ],
        };
      }
      return null;
    },
    "openai.responses": (event, state) => {
      const data = event.data;
      const type = data?.type ?? event.event;
      const keyOf = (item, content) => `r${item}:${content}`;
      if (type === "response.output_text.delta") {
        return {
          key: keyOf(data.item_id, data.content_index),
          text: data.delta ?? "",
          write: (text) => [{ ...event, data: { ...data, delta: text } }],
        };
      }
      if (type === "response.output_text.done") {
        const key = keyOf(data.item_id, data.content_index);
        return {
          key,
          text: "",
          final: true,
          write: (text, full) => [
            ...(text
              ? [
                  {
                    event: "response.output_text.delta",
                    data: {
                      type: "response.output_text.delta",
                      item_id: data.item_id,
                      output_index: data.output_index,
                      content_index: data.content_index,
                      delta: text,
                    },
                  },
                ]
              : []),
            { ...event, data: { ...data, text: full } },
          ],
        };
      }
      // The events that repeat a whole text get what was sent instead.
      const whole = (part, item, content) => {
        const full = state.full?.[keyOf(item, content)];
        return full !== undefined && part?.type === "output_text"
          ? { ...part, text: full }
          : part;
      };
      if (type === "response.content_part.done") {
        return {
          passthrough: [
            {
              ...event,
              data: {
                ...data,
                part: whole(data.part, data.item_id, data.content_index),
              },
            },
          ],
        };
      }
      if (
        type === "response.output_item.done" &&
        Array.isArray(data.item?.content)
      ) {
        const content = data.item.content.map((part, index) =>
          whole(part, data.item.id, index),
        );
        return {
          passthrough: [
            { ...event, data: { ...data, item: { ...data.item, content } } },
          ],
        };
      }
      if (
        type === "response.completed" &&
        Array.isArray(data.response?.output)
      ) {
        const output = data.response.output.map((item) =>
          Array.isArray(item.content)
            ? {
                ...item,
                content: item.content.map((part, index) =>
                  whole(part, item.id, index),
                ),
              }
            : item,
        );
        return {
          passthrough: [
            {
              ...event,
              data: { ...data, response: { ...data.response, output } },
            },
          ],
        };
      }
      return null;
    },
    "google.generate_content": (event) => {
      const candidate = event.data?.candidates?.[0];
      const parts = candidate?.content?.parts;
      if (!Array.isArray(parts)) return null;
      const texts = parts.filter(
        (part) => typeof part.text === "string" && !part.thought,
      );
      return {
        key: "g" + (candidate.index ?? 0),
        text: texts.map((part) => part.text).join(""),
        final: candidate.finishReason != null,
        write: (text) => {
          const data = structuredClone(event.data);
          const kept = data.candidates[0].content.parts.filter(
            (part) => typeof part.text !== "string" || part.thought,
          );
          data.candidates[0].content.parts = [{ text }, ...kept];
          return [{ ...event, data }];
        },
      };
    },
  };

  // textStream makes the stream hooks of a plugin that only changes the
  // reply's visible text: map changes each piece as it comes; replace
  // replaces strings or patterns, also where a piece ends inside one, by
  // holding back as many characters as the longest one may need
  // (maxMatch for a pattern, 64 by default) until the text ends.
  const textStream = (options = {}) => {
    const rules = replacer(options);
    const map = typeof options.map === "function" ? options.map : null;
    const change = (text) => (map && text ? String(map(text)) : text);
    const onStreamEvent = (ctx, event, state) => {
      state = state ?? { pending: {}, full: {} };
      const read = streams[ctx?.protocol]?.(event, state);
      if (!read) return { events: [event], state };
      if (read.passthrough) return { events: read.passthrough, state };
      const previous = state.pending[read.key] ?? "";
      const [out, rest] = flush(
        rules,
        previous + read.text,
        Boolean(read.final),
      );
      const sent = change(out);
      state.pending[read.key] = rest;
      state.full[read.key] = (state.full[read.key] ?? "") + sent;
      if (
        !read.final &&
        out === read.text &&
        rest === "" &&
        previous === "" &&
        sent === read.text
      ) {
        return { events: [event], state };
      }
      return { events: read.write(sent, state.full[read.key]), state };
    };
    const onStreamEnd = (ctx, state) => {
      if (!state) return { events: [] };
      const events = [];
      for (const [key, pending] of Object.entries(state.pending)) {
        if (!pending) continue;
        const [out] = flush(rules, pending, true);
        const text = change(out);
        if (!text) continue;
        // What a stream that ended without its closing event still held.
        if (ctx?.protocol === "openai.chat")
          events.push({
            data: {
              choices: [
                { index: Number(key.slice(1)), delta: { content: text } },
              ],
            },
          });
        else if (ctx?.protocol === "anthropic.messages")
          events.push({
            event: "content_block_delta",
            data: {
              type: "content_block_delta",
              index: Number(key.slice(1)),
              delta: { type: "text_delta", text },
            },
          });
        else if (ctx?.protocol === "google.generate_content")
          events.push({
            data: {
              candidates: [{ content: { role: "model", parts: [{ text }] } }],
            },
          });
      }
      return { events };
    };
    return { onStreamEvent, onStreamEnd };
  };

  if (globalThis.astrlink && typeof globalThis.astrlink === "object") {
    Object.defineProperty(globalThis.astrlink, "textStream", {
      value: textStream,
      enumerable: true,
    });
  }

  define("URL", URL);
  define("URLSearchParams", URLSearchParams);
  define("Headers", Headers);
  define("Response", Response);
  define("AbortController", AbortController);
  define("AbortSignal", AbortSignal);
  define("fetch", fetch);
  define("console", console);
  define("CryptoKey", CryptoKey);
  define("crypto", crypto);
  define("performance", { now: () => host.now() });
})(globalThis.__astrlinkHost);
delete globalThis.__astrlinkHost;
