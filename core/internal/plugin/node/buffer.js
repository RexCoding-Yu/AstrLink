// node:buffer for plugins: the buffer package (internal/vendor/buffer.js),
// with what Node's module has beside Buffer.
(function (module, exports, require) {
  "use strict";
  const vendored = require("internal/vendor/buffer");
  const { Buffer } = vendored;
  const kMaxLength = vendored.kMaxLength;
  const kStringMaxLength = (1 << 29) - 24;
  const strict = new TextDecoder("utf-8", { fatal: true });
  const bytes = (input) =>
    ArrayBuffer.isView(input)
      ? new Uint8Array(input.buffer, input.byteOffset, input.byteLength)
      : new Uint8Array(input);

  // Buffer.copyBytesFrom of Node 19.8.
  if (typeof Buffer.copyBytesFrom !== "function") {
    Buffer.copyBytesFrom = (view, offset = 0, length) => {
      if (!ArrayBuffer.isView(view))
        throw require("internal/errors").invalidArgType(
          "view",
          "an instance of TypedArray",
          view,
        );
      const size = view.BYTES_PER_ELEMENT ?? 1;
      const end = length === undefined ? view.length : offset + length;
      return Buffer.from(
        bytes(view).slice(offset * size, Math.min(end, view.length) * size),
      );
    };
  }

  module.exports = {
    Buffer,
    SlowBuffer: vendored.SlowBuffer,
    INSPECT_MAX_BYTES: vendored.INSPECT_MAX_BYTES,
    kMaxLength,
    kStringMaxLength,
    constants: { MAX_LENGTH: kMaxLength, MAX_STRING_LENGTH: kStringMaxLength },
    atob: globalThis.atob,
    btoa: globalThis.btoa,
    isUtf8(input) {
      try {
        strict.decode(bytes(input));
        return true;
      } catch {
        return false;
      }
    },
    isAscii(input) {
      for (const byte of bytes(input)) if (byte > 127) return false;
      return true;
    },
    transcode(source, fromEncoding, toEncoding) {
      return Buffer.from(Buffer.from(source).toString(fromEncoding), toEncoding);
    },
  };
})
