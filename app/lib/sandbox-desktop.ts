export function isSandboxDesktopEnabled(
  sandboxSpec: Record<string, unknown> | undefined,
  runtimeSpec: Record<string, unknown> | undefined
) {
  if (typeof sandboxSpec?.desktop === "boolean") {
    return sandboxSpec.desktop
  }
  return runtimeSpec?.desktop === true
}
