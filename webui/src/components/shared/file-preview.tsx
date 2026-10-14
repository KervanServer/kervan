import { useEffect, useState } from "react"
import { Download, Loader2 } from "lucide-react"

import { Button } from "@/components/ui/button"
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog"
import { StatusMessage } from "@/components/shared/status-message"
import { formatBytes } from "@/lib/format"
import type { ApiFileEntry } from "@/lib/types"

const TEXT_EXTENSIONS = new Set([
  "txt", "log", "md", "csv", "tsv", "json", "jsonl", "yaml", "yml", "xml", "ini", "conf", "cfg", "toml",
  "env", "sh", "py", "go", "js", "ts", "tsx", "jsx", "css", "html", "sql", "java", "c", "h", "rs", "rb",
])
const IMAGE_EXTENSIONS = new Set(["png", "jpg", "jpeg", "gif", "webp", "bmp", "svg", "ico", "avif"])

/** Largest files previewed in the browser. */
export const MAX_TEXT_PREVIEW = 1 << 20
export const MAX_IMAGE_PREVIEW = 20 << 20

export type PreviewKind = "text" | "image" | null

export function previewKind(entry: Pick<ApiFileEntry, "name" | "size" | "is_dir">): PreviewKind {
  if (entry.is_dir) return null
  const ext = entry.name.includes(".") ? entry.name.split(".").pop()!.toLowerCase() : ""
  if (TEXT_EXTENSIONS.has(ext) && entry.size <= MAX_TEXT_PREVIEW) return "text"
  if (IMAGE_EXTENSIONS.has(ext) && entry.size <= MAX_IMAGE_PREVIEW) return "image"
  return null
}

type Props = {
  entry: ApiFileEntry | null
  load: (entry: ApiFileEntry) => Promise<Blob>
  onDownload: (entry: ApiFileEntry) => void
  onClose: () => void
}

export function FilePreviewDialog({ entry, load, onDownload, onClose }: Props) {
  const [text, setText] = useState<string | null>(null)
  const [imageURL, setImageURL] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)

  useEffect(() => {
    setText(null)
    setImageURL(null)
    setError(null)
    if (!entry) return
    const kind = previewKind(entry)
    let cancelled = false
    let url: string | null = null
    load(entry)
      .then(async (blob) => {
        if (cancelled) return
        if (kind === "image") {
          url = URL.createObjectURL(blob)
          setImageURL(url)
        } else {
          setText(await blob.text())
        }
      })
      .catch((err: unknown) => !cancelled && setError(err instanceof Error ? err.message : "Unable to load the file"))
    return () => {
      cancelled = true
      if (url) URL.revokeObjectURL(url)
    }
  }, [entry, load])

  return (
    <Dialog open={entry !== null} onOpenChange={(open) => (!open ? onClose() : undefined)}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle className="break-all">{entry?.name}</DialogTitle>
          <DialogDescription>
            {entry ? `${formatBytes(entry.size)} · modified ${new Date(entry.mod_time).toLocaleString()}` : ""}
          </DialogDescription>
        </DialogHeader>
        {error ? (
          <StatusMessage variant="error">{error}</StatusMessage>
        ) : imageURL ? (
          <img src={imageURL} alt={entry?.name ?? ""} className="mx-auto max-h-[60vh] max-w-full rounded-lg object-contain" />
        ) : text !== null ? (
          <pre className="max-h-[60vh] overflow-auto whitespace-pre-wrap break-all rounded-lg bg-[var(--muted)] p-3 font-mono text-xs" aria-label="File contents">
            {text}
          </pre>
        ) : (
          <div className="flex justify-center p-8">
            <Loader2 className="h-6 w-6 animate-spin motion-reduce:animate-none" aria-label="Loading preview" />
          </div>
        )}
        <div className="flex justify-end">
          <Button variant="outline" onClick={() => entry && onDownload(entry)}>
            <Download className="mr-2 h-4 w-4" />
            Download
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  )
}
