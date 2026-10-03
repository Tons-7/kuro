// JASSUB's desynchronized canvas becomes an opaque overlay on Android: solid black over the video.
const getContext = OffscreenCanvas.prototype.getContext as (
  this: OffscreenCanvas,
  type: string,
  attributes?: Record<string, unknown>,
) => unknown

OffscreenCanvas.prototype.getContext = function (this: OffscreenCanvas, type: string, attributes?: Record<string, unknown>) {
  return getContext.call(this, type, attributes && { ...attributes, desynchronized: false })
} as typeof OffscreenCanvas.prototype.getContext
