// WebSocket wrapper: binary protobuf, exponential backoff with jitter, and byte counting.
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { ClientMessageSchema, ServerMessageSchema, type Frame, type Hello, type Series, type ServerError, type Subscribe } from "../gen/lens/v1/lens_pb";

export interface SocketHandlers {
  onOpen(): void;
  onClose(attempt: number, retryInMs: number): void;
  onHello(h: Hello): void;
  onFrame(f: Frame, bytes: number): void;
  onSeries(s: Series, bytes: number): void;
  onError(e: ServerError): void;
}

export class LensSocket {
  private ws: WebSocket | null = null;
  private closed = true;
  private attempt = 0;
  private retryTimer = 0;
  private decodeErrorLogged = false;

  constructor(
    private readonly url: () => string,
    private readonly handlers: SocketHandlers,
  ) {}

  get isOpen() {
    return this.ws?.readyState === WebSocket.OPEN;
  }

  open() {
    this.closed = false;
    this.attempt = 0;
    this.connect();
  }

  close() {
    this.closed = true;
    clearTimeout(this.retryTimer);
    this.ws?.close();
    this.ws = null;
  }

  sendSubscribe(sub: Subscribe): boolean {
    if (!this.isOpen) return false;
    this.ws!.send(toBinary(ClientMessageSchema, create(ClientMessageSchema, { msg: { case: "subscribe", value: sub } })));
    return true;
  }

  private connect() {
    const ws = new WebSocket(this.url());
    ws.binaryType = "arraybuffer";
    this.ws = ws;

    ws.onopen = () => {
      this.attempt = 0;
      this.handlers.onOpen();
    };
    ws.onclose = () => {
      if (this.ws !== ws) return;
      this.ws = null;
      if (this.closed) return;
      // Jitter avoids every client reconnecting at the same moment after a restart.
      const delay = Math.min(8000, 500 * 2 ** this.attempt) + Math.random() * 250;
      this.attempt++;
      this.handlers.onClose(this.attempt, delay);
      this.retryTimer = window.setTimeout(() => this.connect(), delay);
    };
    ws.onmessage = (ev) => this.receive(ev.data as ArrayBuffer);
  }

  private receive(data: ArrayBuffer) {
    let msg;
    try {
      msg = fromBinary(ServerMessageSchema, new Uint8Array(data));
    } catch (err) {
      // A bad message must never take the handler down. Skip it, and say so once.
      if (!this.decodeErrorLogged) {
        this.decodeErrorLogged = true;
        console.error("Could not decode a server message, skipping it.", err);
      }
      return;
    }
    const bytes = data.byteLength;
    switch (msg.msg.case) {
      case "hello":
        this.handlers.onHello(msg.msg.value);
        break;
      case "frame":
        this.handlers.onFrame(msg.msg.value, bytes);
        break;
      case "series":
        this.handlers.onSeries(msg.msg.value, bytes);
        break;
      case "error":
        this.handlers.onError(msg.msg.value);
        break;
    }
  }
}
