import {
	Clapperboard,
	Download,
	Film,
	Info,
	Radio,
	RefreshCw,
	Save,
	Trash2,
	Tv,
	Upload,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import { useConfirm } from "../../contexts/ModalContext";
import { useToast } from "../../contexts/ToastContext";
import type { ConfigResponse, TaterBumperLibrary, TaterBumpersConfig } from "../../types/config";

interface TaterBumpersConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: TaterBumpersConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const DEFAULT_TATER_BUMPERS: TaterBumpersConfig = {
	live_tv: true,
	local_movies: true,
	local_series: true,
	nzb_movies: true,
};

function formFromConfig(config: ConfigResponse): TaterBumpersConfig {
	const source = config.tater_bumpers ?? DEFAULT_TATER_BUMPERS;
	return {
		live_tv: source.live_tv ?? true,
		local_movies: source.local_movies ?? true,
		local_series: source.local_series ?? true,
		nzb_movies: source.nzb_movies ?? true,
	};
}

export function TaterBumpersConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: TaterBumpersConfigSectionProps) {
	const { confirmAction } = useConfirm();
	const { showToast } = useToast();
	const [formData, setFormData] = useState<TaterBumpersConfig>(() => formFromConfig(config));
	const [hasChanges, setHasChanges] = useState(false);
	const [library, setLibrary] = useState<TaterBumperLibrary | null>(null);
	const [isLibraryLoading, setIsLibraryLoading] = useState(true);
	const [isUploading, setIsUploading] = useState(false);
	const [deletingName, setDeletingName] = useState("");

	useEffect(() => {
		setFormData(formFromConfig(config));
		setHasChanges(false);
	}, [config]);

	const loadLibrary = useCallback(async () => {
		setIsLibraryLoading(true);
		try {
			setLibrary(await apiClient.getTaterBumpers());
		} catch (error) {
			showToast({
				type: "error",
				title: "Bumper Library Unavailable",
				message:
					error instanceof Error ? error.message : "Unable to load the Tater Bumper library.",
			});
		} finally {
			setIsLibraryLoading(false);
		}
	}, [showToast]);

	useEffect(() => {
		void loadLibrary();
	}, [loadLibrary]);

	const update = (patch: Partial<TaterBumpersConfig>) => {
		const updated = { ...formData, ...patch };
		setFormData(updated);
		const original = formFromConfig(config);
		setHasChanges(
			updated.live_tv !== original.live_tv ||
				updated.local_movies !== original.local_movies ||
				updated.local_series !== original.local_series ||
				updated.nzb_movies !== original.nzb_movies,
		);
	};

	const save = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("tater_bumpers", formData);
		setHasChanges(false);
	};

	const upload = async (files: FileList | null) => {
		if (!files || files.length === 0) return;
		setIsUploading(true);
		try {
			const next = await apiClient.uploadTaterBumpers(files);
			setLibrary(next);
			showToast({
				type: "success",
				title: "Custom Rotation Active",
				message: `${next.customCount} custom bumper${next.customCount === 1 ? "" : "s"} will now be used instead of the built-in rotation.`,
			});
		} catch (error) {
			showToast({
				type: "error",
				title: "Upload Failed",
				message: error instanceof Error ? error.message : "Unable to upload the selected bumpers.",
			});
		} finally {
			setIsUploading(false);
		}
	};

	const remove = async (name: string, title: string) => {
		const isLast = library?.customCount === 1;
		const confirmed = await confirmAction(
			"Delete Custom Tater Bumper",
			isLast
				? `Delete ${title}? This is the last custom bumper, so the built-in Tater Bumpers will automatically return.`
				: `Delete ${title} from the custom Tater Bumper rotation?`,
			{ type: "error", confirmText: "Delete", confirmButtonClass: "btn-error" },
		);
		if (!confirmed) return;
		setDeletingName(name);
		try {
			const next = await apiClient.deleteTaterBumper(name);
			setLibrary(next);
			showToast({
				type: "success",
				title: next.customActive ? "Bumper Removed" : "Built-In Rotation Restored",
				message: next.customActive
					? `${next.customCount} custom bumper${next.customCount === 1 ? " remains" : "s remain"}.`
					: "The server will use the built-in Tater Bumpers again.",
			});
		} catch (error) {
			showToast({
				type: "error",
				title: "Delete Failed",
				message: error instanceof Error ? error.message : "Unable to delete this bumper.",
			});
		} finally {
			setDeletingName("");
		}
	};

	const formatDuration = (seconds: number) => {
		const total = Math.max(0, Math.round(seconds));
		return `${Math.floor(total / 60)}:${String(total % 60).padStart(2, "0")}`;
	};

	const formatSize = (bytes?: number) => {
		if (!bytes || bytes <= 0) return "";
		if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
		if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
		return `${Math.max(1, Math.round(bytes / 1024))} KB`;
	};

	const options = [
		{
			key: "live_tv" as const,
			title: "Live TV",
			description:
				"Include a Tater bumper from the active rotation in server-planned Tube TV breaks.",
			icon: Radio,
		},
		{
			key: "local_movies" as const,
			title: "Local Movies",
			description: "Play one before movies opened from server-local media.",
			icon: Film,
		},
		{
			key: "local_series" as const,
			title: "Local Series",
			description: "Play one before local episodes, including automatic next-episode playback.",
			icon: Tv,
		},
		{
			key: "nzb_movies" as const,
			title: "NZB Movies",
			description: "Play one before a prepared Discovery or NZB movie starts.",
			icon: Download,
		},
	];

	return (
		<div className="min-w-0 space-y-8">
			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
					<div className="min-w-0">
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Server Policy
						</h4>
						<p className="mt-3 max-w-2xl text-base-content/60 text-sm leading-relaxed">
							Choose where the server allows its paired Tater Tube players to use the active Tater
							Bumper rotation.
						</p>
					</div>
					<button
						type="button"
						className="btn btn-primary"
						disabled={!hasChanges || isUpdating || isReadOnly}
						onClick={save}
					>
						{isUpdating ? (
							<span className="loading loading-spinner loading-xs" />
						) : (
							<Save className="h-4 w-4" />
						)}
						Save
					</button>
				</div>

				<div className="grid gap-4 md:grid-cols-2">
					{options.map(({ key, title, description, icon: Icon }) => (
						<label
							key={key}
							className="flex items-start justify-between gap-5 rounded-xl border border-base-300 bg-base-100/70 p-5"
						>
							<div className="flex min-w-0 gap-3">
								<Icon className="mt-0.5 h-5 w-5 shrink-0 text-primary" />
								<div className="min-w-0">
									<div className="font-bold text-sm">{title}</div>
									<p className="mt-1 text-[11px] text-base-content/50 leading-relaxed">
										{description}
									</p>
								</div>
							</div>
							<input
								type="checkbox"
								className="toggle toggle-primary shrink-0"
								checked={formData[key]}
								disabled={isReadOnly}
								onChange={(event) => update({ [key]: event.target.checked })}
							/>
						</label>
					))}
				</div>
			</div>

			<div className="overflow-hidden rounded-2xl border-2 border-base-300/80 bg-base-200/60">
				<div className="flex flex-col gap-5 border-base-300/70 border-b p-6 lg:flex-row lg:items-center lg:justify-between">
					<div className="flex min-w-0 gap-4">
						<div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-xl border border-primary/20 bg-primary/10 text-primary">
							<Clapperboard className="h-5 w-5" />
						</div>
						<div className="min-w-0">
							<div className="flex flex-wrap items-center gap-2">
								<h4 className="font-bold text-base-content text-sm">Custom Tater Bumpers</h4>
								{library && (
									<span
										className={`badge badge-sm ${library.customActive ? "badge-primary" : "badge-ghost"}`}
									>
										{library.customActive ? "Custom rotation active" : "Built-in rotation active"}
									</span>
								)}
							</div>
							<p className="mt-1 max-w-2xl text-[11px] text-base-content/55 leading-relaxed">
								Upload your own short video bumpers. Once one is available, the server pauses the
								built-in rotation and uses only your custom library everywhere Tater Bumpers are
								enabled.
							</p>
						</div>
					</div>
					<div className="flex shrink-0 flex-wrap gap-2">
						<button
							type="button"
							className="btn btn-ghost btn-sm"
							disabled={isLibraryLoading || isUploading}
							onClick={() => void loadLibrary()}
						>
							<RefreshCw className={`h-4 w-4 ${isLibraryLoading ? "animate-spin" : ""}`} />
							Refresh
						</button>
						<label
							className={`btn btn-primary btn-sm ${isReadOnly || isUploading ? "btn-disabled" : ""}`}
						>
							{isUploading ? (
								<span className="loading loading-spinner loading-xs" />
							) : (
								<Upload className="h-4 w-4" />
							)}
							{isUploading ? "Uploading" : "Upload Videos"}
							<input
								type="file"
								className="hidden"
								multiple
								accept="video/*,.mkv,.m4v,.webm,.avi,.mov,.mp4,.ts,.m2ts"
								disabled={isReadOnly || isUploading}
								onChange={(event) => {
									void upload(event.target.files);
									event.target.value = "";
								}}
							/>
						</label>
					</div>
				</div>

				<div className="p-6">
					{isLibraryLoading && !library ? (
						<div className="flex min-h-28 items-center justify-center gap-3 text-base-content/45 text-sm">
							<span className="loading loading-spinner loading-sm" />
							Loading bumper library
						</div>
					) : library?.customActive ? (
						<div className="space-y-3">
							<div className="flex flex-wrap items-center justify-between gap-2 text-[11px] text-base-content/50">
								<span>
									{library.customCount} custom video{library.customCount === 1 ? "" : "s"} in
									rotation
								</span>
								<span>Built-in bumpers are paused</span>
							</div>
							<div className="grid gap-3 md:grid-cols-2">
								{library.videos.map((video) => (
									<div
										key={video.name}
										className="flex min-w-0 items-center gap-3 rounded-xl border border-base-300 bg-base-100/70 p-4"
									>
										<div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-lg bg-primary/10 text-primary">
											<Film className="h-4 w-4" />
										</div>
										<div className="min-w-0 flex-1">
											<div className="truncate font-semibold text-sm">{video.title}</div>
											<div className="mt-1 flex flex-wrap gap-x-2 text-[10px] text-base-content/45">
												<span>{formatDuration(video.duration)}</span>
												{formatSize(video.size) && <span>{formatSize(video.size)}</span>}
												<span className="truncate">{video.name}</span>
											</div>
										</div>
										<button
											type="button"
											className="btn btn-ghost btn-sm btn-square text-error"
											disabled={isReadOnly || deletingName === video.name}
											onClick={() => void remove(video.name, video.title)}
											aria-label={`Delete ${video.title}`}
										>
											{deletingName === video.name ? (
												<span className="loading loading-spinner loading-xs" />
											) : (
												<Trash2 className="h-4 w-4" />
											)}
										</button>
									</div>
								))}
							</div>
						</div>
					) : (
						<div className="flex min-h-28 flex-col items-center justify-center rounded-xl border border-base-300/70 border-dashed bg-base-100/35 px-6 text-center">
							<Clapperboard className="h-7 w-7 text-base-content/30" />
							<div className="mt-3 font-bold text-sm">The Tater originals are in rotation</div>
							<p className="mt-1 max-w-lg text-[11px] text-base-content/50 leading-relaxed">
								Upload a custom video to replace all {library?.builtInCount ?? 0} built-in bumpers.
								Remove the last custom video whenever you want the originals back.
							</p>
						</div>
					)}
				</div>
			</div>

			<div className="alert items-start rounded-2xl border border-info/20 bg-info/5 p-4 shadow-sm">
				<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
				<div className="min-w-0 flex-1">
					<div className="font-bold text-info text-xs uppercase tracking-wider">
						Server-Owned Controls
					</div>
					<div className="mt-1 break-words text-[11px] leading-relaxed opacity-80">
						Paired Apple TV and Android TV players read this policy from the server at playback
						time. Tube TV bumpers remain part of the server-planned channel schedule.
					</div>
				</div>
			</div>
		</div>
	);
}
