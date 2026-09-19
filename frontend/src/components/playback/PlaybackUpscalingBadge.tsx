import { Sparkles } from "lucide-react";
import type { ActiveStream } from "../../types/api";

interface UpscalingPresentation {
	label: string;
	detail: string;
	className: string;
}

export function playbackUpscalingPresentation(stream?: ActiveStream): UpscalingPresentation | null {
	if (!stream?.upscaling_active) return null;

	const requested = String(stream.upscaling_requested || "").toLowerCase();
	const method = String(stream.upscaling_method || "").toLowerCase();
	const fellBack = (requested === "auto" || requested === "ai") && method !== "ai";

	switch (method) {
		case "ai":
			return {
				label: "AI · FSRCNNX",
				detail: "AI upscaling with the FSRCNNX model",
				className: "border-secondary/45 bg-secondary/10 text-secondary",
			};
		case "spline36":
			return {
				label: fellBack ? "AI fallback · Spline36" : "Standard · Spline36",
				detail: fellBack
					? "AI was unavailable, so Standard Spline36 is being used"
					: "Standard upscaling with Spline36",
				className: fellBack
					? "border-warning/45 bg-warning/10 text-warning"
					: "border-info/45 bg-info/10 text-info",
			};
		case "spline":
			return {
				label: fellBack ? "AI fallback · Spline" : "Standard · Spline",
				detail: fellBack
					? "AI was unavailable, so portable Standard Spline is being used"
					: "Standard upscaling with portable Spline",
				className: fellBack
					? "border-warning/45 bg-warning/10 text-warning"
					: "border-info/45 bg-info/10 text-info",
			};
		default:
			return null;
	}
}

export function PlaybackUpscalingBadge({
	stream,
	compact = false,
}: {
	stream?: ActiveStream;
	compact?: boolean;
}) {
	const upscaling = playbackUpscalingPresentation(stream);
	if (!upscaling) return null;

	return (
		<span
			className={`inline-flex max-w-full items-center gap-1 rounded-full border font-semibold ${
				compact ? "px-2 py-0.5 text-[10px]" : "px-2.5 py-1 text-xs"
			} ${upscaling.className}`}
			title={upscaling.detail}
		>
			<Sparkles className={compact ? "h-3 w-3 shrink-0" : "h-3.5 w-3.5 shrink-0"} />
			<span className="truncate">{upscaling.label}</span>
		</span>
	);
}
