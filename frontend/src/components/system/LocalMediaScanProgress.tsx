import { Database, LoaderCircle } from "lucide-react";
import type { LocalMediaScanStatus } from "../../types/config";

interface LocalMediaScanProgressProps {
	status: LocalMediaScanStatus;
	compact?: boolean;
}

function phaseLabel(phase: string) {
	switch (phase) {
		case "discovering":
			return "Preparing library scan";
		case "scanning":
			return "Scanning local library";
		case "durations":
			return "Reading video details";
		case "artwork":
			return "Matching artwork & metadata";
		default:
			return "Updating local library";
	}
}

function progressDetail(status: LocalMediaScanStatus) {
	if (status.phase === "artwork") {
		if (status.videos_total > 0) {
			return `${status.videos_processed.toLocaleString()} of ${status.videos_total.toLocaleString()} movies/shows checked`;
		}
		if (status.albums_total > 0) {
			return `${status.albums_processed.toLocaleString()} of ${status.albums_total.toLocaleString()} albums checked`;
		}
	}
	if (status.progress_total > 0) {
		return `${status.progress_current.toLocaleString()} of ${status.progress_total.toLocaleString()} items`;
	}
	return status.files_scanned > 0
		? `${status.files_scanned.toLocaleString()} files scanned`
		: "Getting the library ready";
}

export function LocalMediaScanProgress({ status, compact = false }: LocalMediaScanProgressProps) {
	const hasMeasuredProgress = status.progress_total > 0;
	const percent = Math.max(0, Math.min(100, status.progress_percent || 0));

	return (
		<div className={`rounded-xl border border-primary/25 bg-primary/10 ${compact ? "p-3" : "p-4"}`}>
			<div className="flex min-w-0 items-start gap-3">
				<div className="relative mt-0.5 shrink-0 rounded-lg bg-primary/15 p-2 text-primary">
					<Database className="h-4 w-4" />
					<LoaderCircle className="-right-1 -top-1 absolute h-3.5 w-3.5 animate-spin rounded-full bg-base-100 text-primary" />
				</div>
				<div className="min-w-0 flex-1">
					<div className="flex min-w-0 items-start justify-between gap-3">
						<div className="min-w-0">
							<div className="font-bold text-sm">{phaseLabel(status.phase)}</div>
							<div className="mt-0.5 truncate text-base-content/60 text-xs">
								{status.message || "Working in the background"}
							</div>
						</div>
						{hasMeasuredProgress && (
							<span className="shrink-0 font-bold text-primary text-sm">{percent}%</span>
						)}
					</div>
					<progress
						className="progress progress-primary mt-3 h-2 w-full"
						value={hasMeasuredProgress ? percent : undefined}
						max={100}
					/>
					<div className="mt-1.5 flex flex-wrap justify-between gap-x-3 gap-y-1 text-[11px] text-base-content/50">
						<span>{progressDetail(status)}</span>
						{status.phase === "artwork" && (
							<span>
								{status.artwork_found.toLocaleString()} images ·{" "}
								{status.metadata_found.toLocaleString()} NFO files found
							</span>
						)}
					</div>
				</div>
			</div>
		</div>
	);
}
