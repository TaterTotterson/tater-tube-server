import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Clapperboard, Clock, RefreshCw, Tv } from "lucide-react";
import { apiClient } from "../api/client";
import { ErrorAlert } from "../components/ui/ErrorAlert";
import { LoadingSpinner } from "../components/ui/LoadingSpinner";
import { useToast } from "../contexts/ToastContext";
import type { TubeTVGuideChannel, TubeTVGuideScheduleItem } from "../types/config";

function formatDuration(seconds?: number) {
	if (!Number.isFinite(seconds) || !seconds || seconds <= 0) return "00:00";
	const totalSeconds = Math.max(0, Math.round(seconds));
	const hours = Math.floor(totalSeconds / 3600);
	const minutes = Math.floor((totalSeconds % 3600) / 60);
	const secs = totalSeconds % 60;
	if (hours > 0)
		return `${hours}:${String(minutes).padStart(2, "0")}:${String(secs).padStart(2, "0")}`;
	return `${minutes}:${String(secs).padStart(2, "0")}`;
}

function formatClock(value?: string) {
	if (!value) return "Not planned";
	const date = new Date(value);
	if (Number.isNaN(date.getTime())) return "Unknown";
	return new Intl.DateTimeFormat(undefined, {
		month: "short",
		day: "numeric",
		hour: "numeric",
		minute: "2-digit",
	}).format(date);
}

function guideElapsedSeconds(startedAt?: string) {
	if (!startedAt) return 0;
	const started = new Date(startedAt);
	if (Number.isNaN(started.getTime())) return 0;
	return Math.max(0, (Date.now() - started.getTime()) / 1000);
}

function currentScheduleItem(channel: TubeTVGuideChannel, elapsed: number) {
	if (!channel.schedule?.length) return null;
	for (let index = 0; index < channel.schedule.length; index++) {
		const row = channel.schedule[index];
		if (elapsed >= (row.start ?? 0) && elapsed < (row.end ?? 0)) {
			return { row, index };
		}
	}
	return null;
}

function upcomingRows(channel: TubeTVGuideChannel, currentIndex: number) {
	if (!channel.schedule?.length) return [];
	const start = currentIndex >= 0 ? currentIndex + 1 : 0;
	return channel.schedule.slice(start, start + 6);
}

function itemKindLabel(row?: TubeTVGuideScheduleItem) {
	const kind = String(row?.kind || row?.mediaType || row?.type || "media").toLowerCase();
	if (kind === "commercial") return "Commercial";
	if (kind === "episode") return "Episode";
	if (kind === "movie") return "Movie";
	return kind.replace(/\b\w/g, (char) => char.toUpperCase());
}

function scheduleTitle(row?: TubeTVGuideScheduleItem) {
	return row?.title || row?.path?.split(/[\\/]/).filter(Boolean).at(-1) || "Untitled";
}

function channelProgress(row: TubeTVGuideScheduleItem | undefined, elapsed: number) {
	if (!row) return 0;
	const start = row.start ?? 0;
	const duration = Math.max(1, (row.end ?? 0) - start);
	return Math.max(0, Math.min(100, ((elapsed - start) / duration) * 100));
}

function GuideStat({
	icon: Icon,
	label,
	value,
}: {
	icon: typeof Tv;
	label: string;
	value: string;
}) {
	return (
		<div className="rounded-lg border border-base-300 bg-base-200/70 p-4">
			<div className="flex items-center gap-3">
				<div className="rounded-md bg-primary/15 p-2">
					<Icon className="h-5 w-5 text-primary" />
				</div>
				<div className="min-w-0">
					<div className="text-base-content/50 text-xs uppercase tracking-widest">{label}</div>
					<div className="mt-1 truncate font-vcr text-lg text-primary">{value}</div>
				</div>
			</div>
		</div>
	);
}

export function TVGuidePage() {
	const queryClient = useQueryClient();
	const { showToast } = useToast();
	const {
		data: guide,
		isLoading,
		error,
		refetch,
	} = useQuery({
		queryKey: ["tube-tv", "guide"],
		queryFn: () => apiClient.getTubeTVGuide(),
		refetchInterval: 30000,
	});
	const rebuildGuide = useMutation({
		mutationFn: () => apiClient.rebuildTubeTVGuide(),
		onSuccess: (data) => {
			queryClient.setQueryData(["tube-tv", "guide"], data);
			showToast({
				type: "success",
				title: "Guide Rebuilt",
				message: `${data.channels.length} channel${data.channels.length === 1 ? "" : "s"} planned.`,
			});
		},
		onError: (err) => {
			showToast({
				type: "error",
				title: "Guide Failed",
				message: err instanceof Error ? err.message : "Unable to rebuild guide.",
			});
		},
	});

	if (isLoading) {
		return (
			<div className="flex min-h-[50vh] items-center justify-center">
				<LoadingSpinner size="lg" />
			</div>
		);
	}

	if (error instanceof Error) {
		return <ErrorAlert error={error} onRetry={() => void refetch()} />;
	}

	const channels = guide?.channels ?? [];
	const elapsed = guideElapsedSeconds(guide?.startedAt);
	const plannedSeconds = guide?.plannedUntil
		? Math.max(0, (new Date(guide.plannedUntil).getTime() - Date.now()) / 1000)
		: 0;

	return (
		<div className="space-y-6">
			<div className="flex flex-col gap-4 lg:flex-row lg:items-start lg:justify-between">
				<div className="min-w-0">
					<div className="mb-2 flex items-center gap-2">
						<Tv className="h-5 w-5 text-primary" />
						<h1 className="tater-glow font-vcr text-3xl text-primary">TV Guide</h1>
					</div>
					<p className="max-w-3xl text-base-content/65">
						Server-planned The Tube channels. Every paired Tater Tube player uses this same channel
						timeline.
					</p>
				</div>
				<button
					type="button"
					className="btn btn-primary"
					onClick={() => rebuildGuide.mutate()}
					disabled={rebuildGuide.isPending}
				>
					{rebuildGuide.isPending ? (
						<LoadingSpinner size="sm" />
					) : (
						<RefreshCw className="h-4 w-4" />
					)}
					Rebuild Guide
				</button>
			</div>

			<div className="grid gap-4 md:grid-cols-4">
				<GuideStat icon={Tv} label="Channels" value={String(channels.length)} />
				<GuideStat
					icon={CalendarClock}
					label="Planned Until"
					value={formatClock(guide?.plannedUntil)}
				/>
				<GuideStat icon={Clock} label="Remaining" value={formatDuration(plannedSeconds)} />
				<GuideStat
					icon={Clapperboard}
					label="Horizon"
					value={`${guide?.horizonHours ?? 12} hours`}
				/>
			</div>

			{channels.length === 0 ? (
				<div className="rounded-lg border border-base-300 bg-base-200/70 p-6 text-base-content/60">
					No TV guide channels are available. Check Local media and The Tube TV Mode settings.
				</div>
			) : (
				<div className="grid gap-4 xl:grid-cols-2">
					{channels.map((channel) => {
						const current = currentScheduleItem(channel, elapsed);
						const currentRow = current?.row;
						const progress = channelProgress(currentRow, elapsed);
						const nextRows = upcomingRows(channel, current?.index ?? -1);

						return (
							<section
								key={channel.number}
								className="min-w-0 rounded-lg border border-base-300 bg-base-200/70 p-5"
							>
								<div className="flex min-w-0 items-start justify-between gap-3">
									<div className="min-w-0">
										<div className="font-vcr text-primary text-xl">CH {channel.number}</div>
										<h2 className="mt-1 truncate font-bold text-lg">{channel.title}</h2>
									</div>
									<div className="badge badge-primary badge-outline">
										{formatDuration(channel.totalDuration)}
									</div>
								</div>

								<div className="mt-4 rounded-lg bg-base-100/75 p-4">
									<div className="flex items-center justify-between gap-3">
										<div className="min-w-0">
											<div className="text-base-content/45 text-xs uppercase tracking-widest">
												Now Playing
											</div>
											<div className="mt-1 truncate font-bold">{scheduleTitle(currentRow)}</div>
										</div>
										<div className="badge badge-secondary badge-outline">
											{itemKindLabel(currentRow)}
										</div>
									</div>
									<progress
										className="progress progress-primary mt-3 h-2 w-full"
										value={progress}
										max="100"
									/>
									<div className="mt-2 flex justify-between text-base-content/50 text-xs">
										<span>{formatDuration(Math.max(0, elapsed - (currentRow?.start ?? 0)))}</span>
										<span>{formatDuration(currentRow?.duration)}</span>
									</div>
								</div>

								<div className="mt-4 space-y-2">
									<div className="text-base-content/45 text-xs uppercase tracking-widest">
										Up Next
									</div>
									{nextRows.length === 0 ? (
										<div className="rounded-md bg-base-100/50 px-3 py-2 text-base-content/50 text-sm">
											Guide extension pending
										</div>
									) : (
										nextRows.map((row) => (
											<div
												key={`${channel.number}-${row.start}-${row.title}`}
												className="flex min-w-0 items-center justify-between gap-3 rounded-md bg-base-100/50 px-3 py-2"
											>
												<div className="min-w-0">
													<div className="truncate font-medium text-sm">{scheduleTitle(row)}</div>
													<div className="text-base-content/45 text-xs">
														{formatDuration(row.start)} / {formatDuration(row.duration)}
													</div>
												</div>
												<span className="badge badge-ghost badge-sm">{itemKindLabel(row)}</span>
											</div>
										))
									)}
								</div>
							</section>
						);
					})}
				</div>
			)}
		</div>
	);
}
