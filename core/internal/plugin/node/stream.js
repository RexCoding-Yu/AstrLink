// node:stream for plugins: the readable-stream package, Node's streams as
// a package (internal/vendor/stream.js), whose module is the legacy Stream
// with the classes and functions on it, as Node's is.
(function (module, exports, require) {
  "use strict";
  const Stream = require("internal/vendor/stream");

  // duplexPair of Node 22.6: two duplexes, each reading what the other
  // writes.
  if (typeof Stream.duplexPair !== "function") {
    Stream.duplexPair = (options) => {
      const sides = [];
      const side = (index) =>
        new Stream.Duplex({
          ...options,
          read() {},
          write(chunk, encoding, callback) {
            sides[1 - index].push(chunk, encoding);
            callback();
          },
          final(callback) {
            sides[1 - index].push(null);
            callback();
          },
        });
      sides.push(side(0), side(1));
      return sides;
    };
  }
  module.exports = Stream;
})
