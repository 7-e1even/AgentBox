export function shellSingleQuote(value: string) {
  return `'${value.replaceAll("'", "'\"'\"'")}'`
}
