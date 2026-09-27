import { Gauge } from "lucide-react";
import type { ActiveStream } from "../../types/api";

function compactDuration(seconds: number) {
	const total = Math.max(0, Math.round(seconds));
	const minutes = Math.floor(total / 60);
	const remainder = total % 60;
	return `${minutes}:${String(remainder).padStart(2, "0")}`;
}

export function PlaybackBufferBadge({
	stream,
	compact = false,
}: {
	stream?: ActiveStream;
	compact?: boolean;
}) {
	const target = Number(stream?.buffer_target_seconds || 0);
	if (!Number.isFinite(target) || target <= 0) return null;

	const buffered = Math.max(0, Number(stream?.buffered_seconds || 0));
	const ratio = Math.min(1, buffered / target);
	const className =
		ratio >= 0.6 ? "badge-success" : ratio >= 0.25 ? "badge-warning" : "badge-error";
	const underruns = Math.max(0, Number(stream?.buffer_underruns || 0));
	const detail = underruns > 0 ? ` · ${underruns} refill${underruns === 1 ? "" : "s"}` : "";

	return (
		<span
			className={`badge ${className} badge-outline gap-1 font-semibold`}
			title={`Server runway ${compactDuration(buffered)} of ${compactDuration(target)}${detail}`}
		>
			<Gauge className="h-3 w-3" />
			{compact ? compactDuration(buffered) : `Buffer ${compactDuration(buffered)}`}
		</span>
	);
}
