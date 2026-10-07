/**
 * noVNC ships no types. Declared here rather than pulled from DefinitelyTyped
 * so the surface stays exactly what this screen uses - anything wider would
 * be a claim about a library we have not read.
 */
declare module "@novnc/novnc" {
  interface RFBOptions {
    credentials?: { username?: string; password?: string; target?: string };
    shared?: boolean;
    repeaterID?: string;
    wsProtocols?: string[];
  }

  export default class RFB extends EventTarget {
    constructor(target: Element, url: string, options?: RFBOptions);
    /** Input off. This screen is for watching until a person says otherwise. */
    viewOnly: boolean;
    /** Fit the remote screen to the element it is drawn in. */
    scaleViewport: boolean;
    /** Never reshape the agent's session because someone is looking at it. */
    resizeSession: boolean;
    /** CSS colour painted behind the framebuffer. */
    background: string;
    disconnect(): void;
  }
}
