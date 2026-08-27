declare module "@novnc/novnc" {
  type RFBOptions = {
    credentials?: {
      username?: string
      password?: string
      target?: string
    }
    shared?: boolean
    repeaterID?: string
    wsProtocols?: string[]
  }

  export default class RFB extends EventTarget {
    constructor(target: HTMLElement, url: string, options?: RFBOptions)

    background: string
    clipViewport: boolean
    focusOnClick: boolean
    resizeSession: boolean
    scaleViewport: boolean
    showDotCursor: boolean
    viewOnly: boolean

    disconnect(): void
    focus(): void
    sendCtrlAltDel(): void
  }
}
