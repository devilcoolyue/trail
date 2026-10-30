export type TrailKind = 'plan' | 'change' | 'ask' | 'debug'

export type TrailFile = {
  path: string
  /** A: created, M: modified */
  op: 'A' | 'M'
  add: number
  del: number
}

/** One round of the conversation: a prompt of the person's and everything up to the next. */
export type TrailTurn = {
  /** The prompt row's uuid: the transcript row's requestId, what a jump scrolls to. */
  id: string
  index: number
  /** ISO timestamps of the prompt and of the turn's last row. */
  time: string
  endTime: string
  prompt: string
  /** The last text the model wrote in the turn. */
  answer: string
  thinkingChars: number
  tools: Record<string, number>
  files: TrailFile[]
  /** Not on the conversation's current branch: rewound with Esc Esc or an edit. */
  isRewound: boolean
  /**
   * Before the conversation's last compaction: a resumed session no longer
   * draws it, so a jump there finds nothing.
   */
  isCompacted: boolean
  kind: TrailKind
  summary: string
  /** The summary came from the model, not from the fallback rules. */
  isSummarized: boolean
}

declare module 'claude-code' {
  interface PluginState {
    trail: {
      turns: Shaped<TrailTurn[]>
      /** '' for all, else a TrailKind */
      filter: string
      query: string
      /** The id of the turn last jumped to, drawn expanded. */
      selected: string
      /** A line about the summaries being made, '' when idle. */
      busy: string
    }
  }
}
