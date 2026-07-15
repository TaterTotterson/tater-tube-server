import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { CalendarClock, Clapperboard, Clock, RefreshCw, Tv } from "lucide-react";
import { apiClient } from "../api/client";
import { ErrorAlert } from "../components/ui/ErrorAlert";
import { LoadingSpinner } from "../components/ui/LoadingSpinner";
import { useToast } from "../contexts/ToastContext";
import type { TubeTVGuideChannel, TubeTVGuideScheduleItem } from "../types/config";

const GUIDE_WINDOW_SECONDS = 3 * 60 * 60;
const GUIDE_SLOT_SECONDS = 30 * 60;
const GUIDE_SLOT_WIDTH = 176;
const GUIDE_CHANNEL_WIDTH = 156;
const GUIDE_SLOT_COUNT = GUIDE_WINDOW_SECONDS / GUIDE_SLOT_SECONDS;
const GUIDE_TIMELINE_WIDTH = GUIDE_SLOT_COUNT * GUIDE_SLOT_WIDTH;

type GuideBlock = {
	row: TubeTVGuideScheduleItem;
	index: number;
	start: number;
	duration: number;
	rowProgress: number;
	isCurrent: boolean;
};

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

function formatTimeLabel(value: Date) {
	return new Intl.DateTimeFormat(undefined, {
		hour: "numeric",
		minute: "2-digit",
	}).format(value);
}

function guideElapsedSeconds(startedAt?: string) {
	if (!startedAt) return 0;
	const started = new Date(startedAt);
	if (Number.isNaN(started.getTime())) return 0;
	return Math.max(0, (Date.now() - started.getTime()) / 1000);
}

function channelPositionSeconds(channel: TubeTVGuideChannel, elapsed: number) {
	const total = Number(channel.totalDuration ?? 0);
	if (!Number.isFinite(total) || total <= 0) return elapsed;
	return Math.max(0, elapsed % total);
}

function findScheduleIndex(channel: TubeTVGuideChannel, position: number) {
	for (let index = 0; index < channel.schedule.length; index++) {
		const row = channel.schedule[index];
		const start = row.start ?? 0;
		const end = row.end ?? start + (row.duration ?? 0);
		if (position >= start && position < end) return index;
	}
	return channel.schedule.length > 0 ? 0 : -1;
}

function currentScheduleItem(channel: TubeTVGuideChannel, elapsed: number) {
	if (!channel.schedule?.length) return null;
	const position = channelPositionSeconds(channel, elapsed);
	const index = findScheduleIndex(channel, position);
	if (index < 0) return null;
	return { row: channel.schedule[index], index };
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

function scheduleDetail(row?: TubeTVGuideScheduleItem) {
	const kind = itemKindLabel(row);
	const duration = formatDuration(row?.duration);
	return duration === "00:00" ? kind : `${kind} / ${duration}`;
}

function channelProgress(row: TubeTVGuideScheduleItem | undefined, elapsed: number) {
	if (!row) return 0;
	const start = row.start ?? 0;
	const duration = Math.max(1, (row.end ?? 0) - start);
	return Math.max(0, Math.min(100, ((elapsed - start) / duration) * 100));
}

function blockWidthPixels(duration: number) {
	return Math.max(1, (duration / GUIDE_SLOT_SECONDS) * GUIDE_SLOT_WIDTH);
}

function guideBlocks(channel: TubeTVGuideChannel, elapsed: number): GuideBlock[] {
	if (!channel.schedule?.length) return [];
	const totalDuration = Number(channel.totalDuration ?? 0);
	if (!Number.isFinite(totalDuration) || totalDuration <= 0) return [];

	const blocks: GuideBlock[] = [];
	let position = channelPositionSeconds(channel, elapsed);
	let cursor = 0;
	let guard = 0;

	while (cursor < GUIDE_WINDOW_SECONDS && guard < channel.schedule.length + 64) {
		const index = findScheduleIndex(channel, position);
		if (index < 0) break;
		const row = channel.schedule[index];
		const rowStart = row.start ?? 0;
		const rowEnd = row.end ?? rowStart + (row.duration ?? 0);
		const remaining = Math.max(1, rowEnd - position);
		const duration = Math.min(remaining, GUIDE_WINDOW_SECONDS - cursor);
		blocks.push({
			row,
			index,
			start: cursor,
			duration,
			rowProgress: channelProgress(row, position),
			isCurrent: cursor === 0,
		});
		cursor += duration;
		position = (position + duration) % totalDuration;
		guard++;
	}

	return blocks;
}

function blockClasses(row: TubeTVGuideScheduleItem, isCurrent: boolean, isCompact: boolean) {
	const kind = String(row.kind || row.mediaType || row.type || "media").toLowerCase();
	const base =
		"absolute top-2 bottom-2 overflow-hidden rounded-md border shadow-inner";
	const padding = isCompact ? " px-0 py-0" : " px-3 py-2";
	const current = isCurrent ? " ring-2 ring-primary/80" : "";
	if (kind === "commercial") {
		return `${base}${padding} border-primary/25 bg-primary/12 text-primary${current}`;
	}
	if (kind === "episode") {
		return `${base}${padding} border-info/30 bg-info/10 text-info-content${current}`;
	}
	if (kind === "movie") {
		return `${base}${padding} border-secondary/35 bg-secondary/12 text-secondary-content${current}`;
	}
	return `${base}${padding} border-base-300 bg-base-200/85 text-base-content${current}`;
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
	const slotLabels = Array.from({ length: GUIDE_SLOT_COUNT }, (_, index) => {
		const date = new Date(Date.now() + index * GUIDE_SLOT_SECONDS * 1000);
		return index === 0 ? "NOW" : formatTimeLabel(date);
	});

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
					No TV guide channels are available. Check Local media and Tube TV settings.
				</div>
			) : (
				<div className="overflow-hidden rounded-lg border border-primary/30 bg-neutral text-neutral-content shadow-inner">
					<div className="border-primary/20 border-b bg-base-300/70 px-4 py-3">
						<div className="flex flex-wrap items-center justify-between gap-3">
							<div className="font-vcr text-primary text-xl">TATER GUIDE</div>
							<div className="font-mono text-neutral-content/65 text-xs uppercase">
								{formatClock(guide?.startedAt)} / {formatDuration(GUIDE_WINDOW_SECONDS)}
							</div>
						</div>
					</div>

					<div className="overflow-x-auto">
						<div style={{ minWidth: GUIDE_CHANNEL_WIDTH + GUIDE_TIMELINE_WIDTH }}>
							<div
								className="grid border-primary/20 border-b bg-base-300/90"
								style={{
									gridTemplateColumns: `${GUIDE_CHANNEL_WIDTH}px ${GUIDE_TIMELINE_WIDTH}px`,
								}}
							>
								<div className="border-primary/20 border-r px-4 py-3 font-mono text-neutral-content/55 text-xs uppercase">
									Channel
								</div>
								<div
									className="grid"
									style={{ gridTemplateColumns: `repeat(${GUIDE_SLOT_COUNT}, ${GUIDE_SLOT_WIDTH}px)` }}
								>
									{slotLabels.map((label, index) => (
										<div
											key={`${label}-${index}`}
											className="border-primary/20 border-r px-3 py-3 font-vcr text-primary text-sm"
										>
											{label}
										</div>
									))}
								</div>
							</div>

							{channels.map((channel) => {
								const current = currentScheduleItem(channel, elapsed);
								const blocks = guideBlocks(channel, elapsed);
								return (
									<div
										key={channel.number}
										className="grid border-primary/15 border-b last:border-b-0"
										style={{
											gridTemplateColumns: `${GUIDE_CHANNEL_WIDTH}px ${GUIDE_TIMELINE_WIDTH}px`,
										}}
									>
										<div className="border-primary/20 border-r bg-base-300/60 px-4 py-3">
											<div className="font-vcr text-primary text-2xl leading-none">
												CH {channel.number}
											</div>
											<div className="mt-2 line-clamp-2 font-bold text-neutral-content text-sm">
												{channel.title}
											</div>
											<div className="mt-2 truncate text-neutral-content/45 text-xs">
												{itemKindLabel(current?.row)}
											</div>
										</div>

										<div className="relative h-28 bg-base-100/80">
											{Array.from({ length: GUIDE_SLOT_COUNT + 1 }, (_, index) => (
												<div
													key={`line-${channel.number}-${index}`}
													className="absolute top-0 bottom-0 border-primary/10 border-l"
													style={{ left: index * GUIDE_SLOT_WIDTH }}
												/>
											))}
											<div className="absolute top-0 bottom-0 left-0 w-1 bg-primary shadow-[0_0_14px_rgba(255,122,0,0.8)]" />
											{blocks.map((block) => {
												const left = (block.start / GUIDE_SLOT_SECONDS) * GUIDE_SLOT_WIDTH;
												const width = blockWidthPixels(block.duration);
												const isCompact = width < 56;
												const showProgress = block.isCurrent && width >= 28;
												return (
													<div
														key={`${channel.number}-${block.index}-${block.start}`}
														className={blockClasses(block.row, block.isCurrent, isCompact)}
														style={{ left, width }}
														title={`${scheduleTitle(block.row)} - ${scheduleDetail(block.row)}`}
													>
														{!isCompact && (
															<>
																<div className="truncate font-bold text-sm">{scheduleTitle(block.row)}</div>
																<div className="mt-1 truncate font-mono text-[11px] opacity-70">
																	{scheduleDetail(block.row)}
																</div>
															</>
														)}
														{showProgress && (
															<div className="absolute right-2 bottom-2 left-2 h-1 overflow-hidden rounded-full bg-black/30">
																<div
																	className="h-full rounded-full bg-primary"
																	style={{ width: `${block.rowProgress}%` }}
																/>
															</div>
														)}
													</div>
												);
											})}
										</div>
									</div>
								);
							})}
						</div>
					</div>
				</div>
			)}
		</div>
	);
}
