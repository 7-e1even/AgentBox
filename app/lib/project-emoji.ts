const graphemeSegmenter = new Intl.Segmenter("zh-CN", {
  granularity: "grapheme",
})

export function getProjectEmoji(project: {
  id: string
  spec: Record<string, unknown>
}) {
  const configured =
    typeof project.spec.emoji === "string" ? project.spec.emoji : ""

  if (configured) {
    const firstGrapheme = graphemeSegmenter.segment(configured.trim())[
      Symbol.iterator
    ]().next().value?.segment

    if (firstGrapheme) return firstGrapheme
  }

  return "📁"
}
