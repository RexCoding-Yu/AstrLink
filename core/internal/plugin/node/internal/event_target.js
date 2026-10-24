// EventTarget, Event and CustomEvent, the globals of Node that the web APIs
// of the host leave out: dispatch to the listeners of a type in order, with
// once, signal and the handleEvent objects.
(function (module) {
  "use strict";
  class Event {
    #type;
    #bubbles;
    #cancelable;
    #composed;
    #defaultPrevented = false;
    #stopped = false;
    #timeStamp = performance.now();
    target = null;
    currentTarget = null;
    eventPhase = 0;
    constructor(type, init = {}) {
      if (arguments.length === 0)
        throw new TypeError('The "type" argument must be specified');
      this.#type = String(type);
      this.#bubbles = Boolean(init?.bubbles);
      this.#cancelable = Boolean(init?.cancelable);
      this.#composed = Boolean(init?.composed);
    }
    get type() {
      return this.#type;
    }
    get bubbles() {
      return this.#bubbles;
    }
    get cancelable() {
      return this.#cancelable;
    }
    get composed() {
      return this.#composed;
    }
    get defaultPrevented() {
      return this.#cancelable && this.#defaultPrevented;
    }
    get timeStamp() {
      return this.#timeStamp;
    }
    get isTrusted() {
      return false;
    }
    get returnValue() {
      return !this.defaultPrevented;
    }
    get cancelBubble() {
      return this.#stopped;
    }
    composedPath() {
      return this.currentTarget ? [this.currentTarget] : [];
    }
    preventDefault() {
      this.#defaultPrevented = true;
    }
    stopPropagation() {}
    stopImmediatePropagation() {
      this.#stopped = true;
    }
    get [Symbol.toStringTag]() {
      return "Event";
    }
    static NONE = 0;
    static CAPTURING_PHASE = 1;
    static AT_TARGET = 2;
    static BUBBLING_PHASE = 3;
  }

  class CustomEvent extends Event {
    #detail;
    constructor(type, init = {}) {
      super(type, init);
      this.#detail = init?.detail ?? null;
    }
    get detail() {
      return this.#detail;
    }
    get [Symbol.toStringTag]() {
      return "CustomEvent";
    }
  }

  class EventTarget {
    #listeners = new Map();
    addEventListener(type, listener, options = {}) {
      if (listener == null) return;
      const once = typeof options === "object" && Boolean(options?.once);
      const capture =
        typeof options === "boolean" ? options : Boolean(options?.capture);
      const signal = typeof options === "object" ? options?.signal : undefined;
      if (signal?.aborted) return;
      type = String(type);
      let list = this.#listeners.get(type);
      if (list === undefined) this.#listeners.set(type, (list = []));
      if (list.some((each) => each.listener === listener && each.capture === capture))
        return;
      const entry = { listener, once, capture };
      list.push(entry);
      signal?.addEventListener("abort", () =>
        this.removeEventListener(type, listener, { capture }),
      );
    }
    removeEventListener(type, listener, options = {}) {
      const capture =
        typeof options === "boolean" ? options : Boolean(options?.capture);
      const list = this.#listeners.get(String(type));
      if (list === undefined) return;
      const index = list.findIndex(
        (each) => each.listener === listener && each.capture === capture,
      );
      if (index !== -1) list.splice(index, 1);
    }
    dispatchEvent(event) {
      if (!(event instanceof Event))
        throw new TypeError('The "event" argument must be an instance of Event');
      const list = this.#listeners.get(event.type);
      event.target = this;
      event.currentTarget = this;
      event.eventPhase = 2;
      for (const entry of list ? [...list] : []) {
        if (entry.once) this.removeEventListener(event.type, entry.listener, entry);
        try {
          if (typeof entry.listener === "function") entry.listener.call(this, event);
          else entry.listener.handleEvent?.(event);
        } catch (error) {
          queueMicrotask(() => {
            throw error;
          });
        }
        if (event.cancelBubble) break;
      }
      event.currentTarget = null;
      event.eventPhase = 0;
      return !event.defaultPrevented;
    }
    get [Symbol.toStringTag]() {
      return "EventTarget";
    }
  }

  module.exports = { Event, CustomEvent, EventTarget };
})
