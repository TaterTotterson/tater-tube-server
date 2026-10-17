import { Sun } from "lucide-react";
import type { ActiveStream } from "../../types/api";

const STRENGTH_LABELS: Record<string, string> = {
	conservative: "Conservative",
	balanced: "Balanced",
	vivid: "Vivid",
};

export function PlaybackHDREnhancementBadge({
	stream,
	compact = false,
}: {
	stream?: ActiveStream;
	compact?: boolean;
}) {
	if (!stream?.hdr_enhancement_active) return null;
	const strength = String(stream.hdr_enhancement || "balanced").toLowerCase();
	const label = STRENGTH_LABELS[strength] || "Balanced";
	return (
		<span
			className={`inline-flex max-w-full items-center gap-1 rounded-full border border-warning/45 bg-warning/10 font-semibold text-warning ${
				compact ? "px-2 py-0.5 text-[10px]" : "px-2.5 py-1 text-xs"
			}`}
			title={`SDR is being enhanced into HDR10 with the ${label} preset`}
		>
			<Sun className={compact ? "h-3 w-3 shrink-0" : "h-3.5 w-3.5 shrink-0"} />
			<span className="truncate">HDR Enhanced · {label}</span>
		</span>
	);
}
