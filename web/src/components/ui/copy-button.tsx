import { useState } from "react";
import { Check, Copy } from "lucide-react";
import { toast } from "sonner";
import { Button } from "@/components/ui/button";
import { copyText } from "@/lib/clipboard";

export async function copyWithToast(text: string, what: string): Promise<boolean> {
  const ok = await copyText(text);
  if (ok) toast.success(`Copied ${what}`);
  else toast.error(`Could not copy ${what}. Select the text and copy it by hand.`);
  return ok;
}

export function CopyButton({
  text,
  what,
  label = "Copy",
}: {
  text: string;
  what: string;
  label?: string;
}) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="border-border text-text-secondary"
      aria-label={`Copy ${what}`}
      onClick={async () => {
        if (await copyWithToast(text, what)) {
          setCopied(true);
          setTimeout(() => setCopied(false), 1500);
        }
      }}
    >
      {copied ? <Check className="size-3 text-accent" /> : <Copy className="size-3" />}
      {copied ? "Copied" : label}
    </Button>
  );
}
