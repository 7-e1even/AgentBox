import type { SVGProps } from "react"

import { cn } from "@/lib/utils"

export function AgentBoxMark({
  className,
  ...props
}: SVGProps<SVGSVGElement>) {
  return (
    <svg
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.75"
      strokeLinecap="round"
      strokeLinejoin="round"
      className={cn("size-5", className)}
      aria-hidden="true"
      {...props}
    >
      <path d="M4 8.25 12 3.75l8 4.5v7.5l-8 4.5-8-4.5v-7.5Z" />
      <path d="m4 8.25 8 4.5 8-4.5M12 12.75v7.5" />
      <path
        d="m12 6.55 2.75 1.55L12 9.65 9.25 8.1 12 6.55Z"
        fill="currentColor"
        stroke="none"
      />
    </svg>
  )
}
