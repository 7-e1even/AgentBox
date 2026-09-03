export function isSandboxDesktopEnabled(
  sandboxSpec: Record<string, unknown> | undefined
) {
  return sandboxSpec?.desktop === true
}
