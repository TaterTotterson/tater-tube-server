import {
	ArrowLeft,
	Check,
	Clapperboard,
	Film,
	Folder,
	Layers,
	Plus,
	RefreshCw,
	Save,
	Search,
	Trash2,
	Tv,
	Upload,
} from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import { useConfirm } from "../../contexts/ModalContext";
import { useToast } from "../../contexts/ToastContext";
import type {
	ConfigResponse,
	LocalMediaCategory,
	TubeTVCommercialLibrary,
	TubeTVConfig,
	TubeTVCustomChannel,
	TubeTVCustomSource,
	TubeTVLocalLibraryResponse,
	TubeTVLocalLibraryRow,
} from "../../types/config";

interface TubeTVConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: TubeTVConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const DEFAULT_TUBE_TV: TubeTVConfig = {
	enabled: true,
	auto_channels: true,
	commercials_enabled: true,
	midroll_commercials: false,
	commercial_categories: [],
	commercials_path: "",
	custom_channels: [],
};

interface LibraryRequest {
	categoryId: string;
	sourceIndex: number;
	path: string;
}

function slug(value: string) {
	return value
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 64);
}

function normalizeSourceCategoryId(value: string) {
	const trimmed = value.trim();
	if (trimmed.startsWith("local-discover:")) {
		return trimmed;
	}
	if (trimmed.startsWith("local:")) {
		return slug(trimmed.slice(6));
	}
	return slug(trimmed);
}

function cleanDisplay(value: string) {
	return value
		.replace(/[-_]+/g, " ")
		.replace(/\s+/g, " ")
		.trim()
		.replace(/\b\w/g, (char) => char.toUpperCase());
}

function normalize(config: ConfigResponse): TubeTVConfig {
	const source = config.tube_tv ?? DEFAULT_TUBE_TV;
	return {
		enabled: source.enabled ?? true,
		auto_channels: source.auto_channels ?? true,
		commercials_enabled: source.commercials_enabled ?? true,
		midroll_commercials: source.midroll_commercials ?? false,
		commercial_categories: source.commercial_categories ?? [],
		commercials_path: source.commercials_path ?? "",
		custom_channels: (source.custom_channels ?? []).map((channel) => ({
			id: channel.id || slug(channel.title || "channel"),
			title: channel.title || "Custom Channel",
			commercial_category: channel.commercial_category || "",
			sources: (channel.sources ?? []).map((row) => ({
				category_id: row.category_id || "",
				source_index: row.source_index ?? -1,
				path: row.path || "",
				title: row.title || "",
				media_type: row.media_type || "",
			})),
		})),
	};
}

function sourceLabel(source: TubeTVCustomSource, categories: LocalMediaCategory[]) {
	const category = source.category_id.startsWith("local-discover:")
		? cleanDisplay(
				source.category_id
					.replace("local-discover:", "")
					.replace("genre:", "")
					.replace("decade:", ""),
			)
		: categories.find((row) => row.id === source.category_id)?.name ||
			cleanDisplay(source.category_id);
	const pathParts = source.path ? source.path.split("/") : [];
	const title =
		source.title ||
		(pathParts.length > 0 ? cleanDisplay(pathParts[pathParts.length - 1] || "") : "");
	return title ? `${category} / ${title}` : category;
}

function libraryRowIcon(row: TubeTVLocalLibraryRow) {
	const kind = `${row.mediaType || ""} ${row.type || ""}`.toLowerCase();
	if (kind.includes("movie") || kind.includes("file")) return <Film className="h-5 w-5" />;
	if (
		kind.includes("tv") ||
		kind.includes("show") ||
		kind.includes("season") ||
		kind.includes("episode")
	) {
		return <Tv className="h-5 w-5" />;
	}
	if (kind.includes("discover") || kind.includes("genre") || kind.includes("decade"))
		return <Layers className="h-5 w-5" />;
	return <Folder className="h-5 w-5" />;
}

export function TubeTVConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: TubeTVConfigSectionProps) {
	const { showToast } = useToast();
	const { confirmAction } = useConfirm();
	const [formData, setFormData] = useState<TubeTVConfig>(() => normalize(config));
	const [library, setLibrary] = useState<TubeTVCommercialLibrary | null>(null);
	const [newCategory, setNewCategory] = useState("");
	const [uploadCategory, setUploadCategory] = useState("");
	const [isLibraryLoading, setIsLibraryLoading] = useState(false);
	const [isUploading, setIsUploading] = useState(false);
	const [browserChannelIndex, setBrowserChannelIndex] = useState<number | null>(null);
	const [browserRequest, setBrowserRequest] = useState<LibraryRequest>({
		categoryId: "",
		sourceIndex: -1,
		path: "",
	});
	const [browserData, setBrowserData] = useState<TubeTVLocalLibraryResponse | null>(null);
	const [browserSearch, setBrowserSearch] = useState("");
	const [isBrowserLoading, setIsBrowserLoading] = useState(false);
	const [hasChanges, setHasChanges] = useState(false);

	const localCategories = (config.local_media?.categories ?? []).filter(
		(category) => category.enabled !== false && category.library_type !== "music",
	);

	useEffect(() => {
		setFormData(normalize(config));
		setHasChanges(false);
	}, [config]);

	const refreshLibrary = useCallback(async () => {
		setIsLibraryLoading(true);
		try {
			const data = await apiClient.getTubeTVCommercials();
			setLibrary(data);
			setUploadCategory((current) => current || data.categories[0]?.id || "");
		} catch (error) {
			showToast({
				type: "error",
				title: "Commercials Failed",
				message: error instanceof Error ? error.message : "Unable to load commercials.",
			});
		} finally {
			setIsLibraryLoading(false);
		}
	}, [showToast]);

	useEffect(() => {
		void refreshLibrary();
	}, [refreshLibrary]);

	useEffect(() => {
		if (browserChannelIndex === null) {
			return;
		}
		let isCurrent = true;
		setIsBrowserLoading(true);
		apiClient
			.getTubeTVLocalLibrary(browserRequest)
			.then((data) => {
				if (isCurrent) setBrowserData(data);
			})
			.catch((error) => {
				if (!isCurrent) return;
				showToast({
					type: "error",
					title: "Library Failed",
					message: error instanceof Error ? error.message : "Unable to load local library.",
				});
			})
			.finally(() => {
				if (isCurrent) setIsBrowserLoading(false);
			});
		return () => {
			isCurrent = false;
		};
	}, [browserChannelIndex, browserRequest, showToast]);

	const update = (next: TubeTVConfig) => {
		setFormData(next);
		setHasChanges(JSON.stringify(next) !== JSON.stringify(normalize(config)));
	};

	const updateChannel = (index: number, patch: Partial<TubeTVCustomChannel>) => {
		const custom_channels = formData.custom_channels.map((channel, i) => {
			if (i !== index) return channel;
			const next = { ...channel, ...patch };
			if (patch.title !== undefined && (!next.id || next.id === slug(channel.title))) {
				next.id = slug(patch.title) || next.id;
			}
			return next;
		});
		update({ ...formData, custom_channels });
	};

	const addChannel = () => {
		const count = formData.custom_channels.length + 1;
		update({
			...formData,
			custom_channels: formData.custom_channels.concat([
				{
					id: `custom-${count}`,
					title: `Custom ${count}`,
					commercial_category: "",
					sources: [],
				},
			]),
		});
	};

	const removeChannel = (index: number) => {
		update({
			...formData,
			custom_channels: formData.custom_channels.filter((_, i) => i !== index),
		});
	};

	const removeSource = (channelIndex: number, sourceIndex: number) => {
		const channel = formData.custom_channels[channelIndex];
		const sources = channel.sources.filter((_, i) => i !== sourceIndex);
		updateChannel(channelIndex, { sources });
	};

	const openBrowser = (channelIndex: number) => {
		setBrowserChannelIndex(channelIndex);
		setBrowserSearch("");
		setBrowserRequest({ categoryId: "", sourceIndex: -1, path: "" });
	};

	const closeBrowser = () => {
		setBrowserChannelIndex(null);
		setBrowserData(null);
		setBrowserSearch("");
	};

	const browseRow = (row: TubeTVLocalLibraryRow) => {
		const categoryId = row.type === "localDiscover" ? row.id || "" : row.categoryId || row.id || "";
		if (!categoryId) return;
		setBrowserSearch("");
		setBrowserRequest({
			categoryId,
			sourceIndex: row.sourceIndex ?? -1,
			path: row.path || "",
		});
	};

	const goBrowserBack = () => {
		if (!browserRequest.categoryId) return;
		if (!browserRequest.path) {
			setBrowserRequest({ categoryId: "", sourceIndex: -1, path: "" });
			setBrowserSearch("");
			return;
		}
		const parts = browserRequest.path.split("/").filter(Boolean);
		setBrowserRequest({
			...browserRequest,
			path: parts.slice(0, -1).join("/"),
		});
		setBrowserSearch("");
	};

	const sourceFromRow = (row: TubeTVLocalLibraryRow): TubeTVCustomSource => {
		const rawCategory =
			row.type === "localDiscover" ? row.id || "" : row.categoryId || row.id || "";
		return {
			category_id: rawCategory.startsWith("local:") ? rawCategory.slice(6) : rawCategory,
			source_index: row.sourceIndex ?? -1,
			path: row.path || "",
			title: row.title || "",
			media_type: row.mediaType || "",
		};
	};

	const addSourceToChannel = (channelIndex: number, source: TubeTVCustomSource) => {
		const normalized: TubeTVCustomSource = {
			...source,
			category_id: normalizeSourceCategoryId(source.category_id),
			path: (source.path || "").replace(/^\/+/, ""),
			title: source.title || "",
			media_type: (source.media_type || "").toLowerCase(),
			source_index: Number.isFinite(Number(source.source_index)) ? Number(source.source_index) : -1,
		};
		if (!normalized.category_id) return;
		const channel = formData.custom_channels[channelIndex];
		const duplicate = channel.sources.some(
			(row) =>
				normalizeSourceCategoryId(row.category_id) === normalized.category_id &&
				Number(row.source_index ?? -1) === normalized.source_index &&
				(row.path || "") === normalized.path,
		);
		if (duplicate) {
			showToast({
				type: "info",
				title: "Already Added",
				message: `${sourceLabel(normalized, localCategories)} is already in this channel.`,
			});
			return;
		}
		updateChannel(channelIndex, { sources: channel.sources.concat([normalized]) });
	};

	const addCurrentBrowserView = (channelIndex: number) => {
		if (!browserData?.source) return;
		addSourceToChannel(channelIndex, browserData.source);
	};

	const toggleCommercialCategory = (categoryId: string) => {
		const set = new Set(formData.commercial_categories);
		if (set.has(categoryId)) set.delete(categoryId);
		else set.add(categoryId);
		update({ ...formData, commercial_categories: Array.from(set) });
	};

	const save = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("tube_tv", {
			...formData,
			commercial_categories: formData.commercial_categories.map(slug).filter(Boolean),
			custom_channels: formData.custom_channels
				.map((channel) => ({
					...channel,
					id: slug(channel.id || channel.title),
					title: channel.title.trim(),
					commercial_category: slug(channel.commercial_category || ""),
					sources: channel.sources
						.map((source) => ({
							category_id: normalizeSourceCategoryId(source.category_id),
							source_index: Number.isFinite(Number(source.source_index))
								? Number(source.source_index)
								: -1,
							path: source.path.trim().replace(/^\/+/, ""),
							title: (source.title || "").trim(),
							media_type: (source.media_type || "").trim().toLowerCase(),
						}))
						.filter((source) => source.category_id),
				}))
				.filter((channel) => channel.id && channel.title),
		});
		setHasChanges(false);
	};

	const createCategory = async () => {
		const name = newCategory.trim();
		if (!name) return;
		try {
			const data = await apiClient.createTubeTVCommercialCategory(name);
			setLibrary(data);
			setUploadCategory(slug(name));
			setNewCategory("");
		} catch (error) {
			showToast({
				type: "error",
				title: "Category Failed",
				message: error instanceof Error ? error.message : "Unable to create category.",
			});
		}
	};

	const uploadFiles = async (files: FileList | null) => {
		if (!files || files.length === 0 || !uploadCategory) return;
		setIsUploading(true);
		try {
			const data = await apiClient.uploadTubeTVCommercials(uploadCategory, files);
			setLibrary(data);
			showToast({
				type: "success",
				title: "Commercials Uploaded",
				message: `${files.length} file${files.length === 1 ? "" : "s"} added.`,
			});
		} catch (error) {
			showToast({
				type: "error",
				title: "Upload Failed",
				message: error instanceof Error ? error.message : "Unable to upload commercials.",
			});
		} finally {
			setIsUploading(false);
		}
	};

	const deleteCategory = async (categoryId: string, title: string) => {
		const confirmed = await confirmAction(
			"Delete Commercial Category",
			`Delete ${title}? This removes the uploaded videos in that category.`,
			{ type: "error", confirmText: "Delete", confirmButtonClass: "btn-error" },
		);
		if (!confirmed) return;
		const data = await apiClient.deleteTubeTVCommercialCategory(categoryId);
		setLibrary(data);
	};

	const browserRows = (browserData?.rows ?? []).filter((row) => {
		const query = browserSearch.trim().toLowerCase();
		if (!query) return true;
		return `${row.title} ${row.detail || ""} ${row.mediaType || ""}`.toLowerCase().includes(query);
	});

	return (
		<div className="min-w-0 space-y-8">
			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
					<div className="min-w-0">
						<div className="mb-3 flex items-center gap-2">
							<Clapperboard className="h-4 w-4 text-base-content/60" />
							<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
								Tube TV
							</h4>
						</div>
						<p className="max-w-2xl text-base-content/60 text-sm leading-relaxed">
							Build Tube TV on the server so every paired Tater Tube player receives the same
							channels, commercials, and custom lineups.
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

				<div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
					<label className="flex items-center justify-between gap-3 rounded-xl border border-base-300 bg-base-100/70 p-4">
						<span className="font-bold text-sm">Enabled</span>
						<input
							type="checkbox"
							className="toggle toggle-primary"
							checked={formData.enabled}
							disabled={isReadOnly}
							onChange={(event) => update({ ...formData, enabled: event.target.checked })}
						/>
					</label>
					<label className="flex items-center justify-between gap-3 rounded-xl border border-base-300 bg-base-100/70 p-4">
						<span className="font-bold text-sm">Auto Channels</span>
						<input
							type="checkbox"
							className="toggle toggle-primary"
							checked={formData.auto_channels}
							disabled={isReadOnly}
							onChange={(event) => update({ ...formData, auto_channels: event.target.checked })}
						/>
					</label>
					<label className="flex items-center justify-between gap-3 rounded-xl border border-base-300 bg-base-100/70 p-4">
						<span className="font-bold text-sm">Commercial Breaks</span>
						<input
							type="checkbox"
							className="toggle toggle-primary"
							checked={formData.commercials_enabled}
							disabled={isReadOnly}
							onChange={(event) =>
								update({ ...formData, commercials_enabled: event.target.checked })
							}
						/>
					</label>
					<label className="flex items-center justify-between gap-3 rounded-xl border border-base-300 bg-base-100/70 p-4">
						<span className="font-bold text-sm">Mid-rolls</span>
						<input
							type="checkbox"
							className="toggle toggle-primary"
							checked={formData.midroll_commercials}
							disabled={isReadOnly}
							onChange={(event) =>
								update({ ...formData, midroll_commercials: event.target.checked })
							}
						/>
					</label>
				</div>

				<label className="form-control mt-5">
					<span className="label-text font-bold text-base-content text-sm">
						Commercial Storage Path
					</span>
					<input
						type="text"
						className="input input-bordered mt-2 w-full"
						value={formData.commercials_path}
						disabled={isReadOnly}
						onChange={(event) => update({ ...formData, commercials_path: event.target.value })}
						placeholder="/config/metadata/tube-tv-commercials"
					/>
				</label>
			</div>

			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
					<div>
						<div className="mb-3 flex items-center gap-2">
							<Upload className="h-4 w-4 text-base-content/60" />
							<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
								Commercial Library
							</h4>
						</div>
						<p className="text-base-content/60 text-sm">
							Upload local commercial videos and pick which categories Tube TV can use.
						</p>
					</div>
					<button
						type="button"
						className="btn btn-outline btn-sm"
						onClick={refreshLibrary}
						disabled={isLibraryLoading}
					>
						{isLibraryLoading ? (
							<span className="loading loading-spinner loading-xs" />
						) : (
							<RefreshCw className="h-4 w-4" />
						)}
						Refresh
					</button>
				</div>

				<div className="grid gap-3 md:grid-cols-[1fr_auto]">
					<input
						type="text"
						className="input input-bordered"
						value={newCategory}
						onChange={(event) => setNewCategory(event.target.value)}
						placeholder="Cartoon Network Commercials"
					/>
					<button type="button" className="btn btn-outline" onClick={createCategory}>
						<Plus className="h-4 w-4" />
						Add Category
					</button>
				</div>

				<div className="mt-4 grid gap-3 md:grid-cols-[16rem_1fr] md:items-end">
					<label className="form-control">
						<span className="label-text font-bold text-base-content text-sm">Upload To</span>
						<select
							className="select select-bordered mt-2"
							value={uploadCategory}
							onChange={(event) => setUploadCategory(event.target.value)}
						>
							<option value="">Select category</option>
							{(library?.categories ?? []).map((category) => (
								<option key={category.id} value={category.id}>
									{category.title}
								</option>
							))}
						</select>
					</label>
					<label
						className={`btn btn-primary ${!uploadCategory || isUploading ? "btn-disabled" : ""}`}
					>
						{isUploading ? (
							<span className="loading loading-spinner loading-xs" />
						) : (
							<Upload className="h-4 w-4" />
						)}
						Upload Videos
						<input
							type="file"
							className="hidden"
							accept="video/*"
							multiple
							disabled={!uploadCategory || isUploading}
							onChange={(event) => void uploadFiles(event.target.files)}
						/>
					</label>
				</div>

				<div className="mt-5 space-y-3">
					{(library?.categories ?? []).length === 0 && (
						<div className="rounded-xl border border-base-300 bg-base-100/70 p-4 text-base-content/60 text-sm">
							No commercial categories yet.
						</div>
					)}
					{(library?.categories ?? []).map((category) => (
						<div key={category.id} className="rounded-xl border border-base-300 bg-base-100/70 p-4">
							<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
								<label className="flex items-center gap-3">
									<input
										type="checkbox"
										className="checkbox checkbox-primary"
										checked={formData.commercial_categories.includes(category.id)}
										onChange={() => toggleCommercialCategory(category.id)}
									/>
									<span className="font-bold">{category.title}</span>
									<span className="badge badge-ghost">{category.count} videos</span>
								</label>
								<button
									type="button"
									className="btn btn-error btn-outline btn-xs"
									onClick={() => void deleteCategory(category.id, category.title)}
								>
									<Trash2 className="h-3 w-3" />
									Delete
								</button>
							</div>
							{category.videos.length > 0 && (
								<div className="mt-3 grid gap-2 sm:grid-cols-2">
									{category.videos.slice(0, 6).map((video) => (
										<div
											key={`${category.id}-${video.name}`}
											className="truncate rounded-lg bg-base-200 px-3 py-2 text-base-content/60 text-xs"
										>
											{video.title}
										</div>
									))}
									{category.videos.length > 6 && (
										<div className="rounded-lg bg-base-200 px-3 py-2 text-base-content/50 text-xs">
											+{category.videos.length - 6} more
										</div>
									)}
								</div>
							)}
						</div>
					))}
				</div>
			</div>

			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-5 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
					<div>
						<div className="mb-3 flex items-center gap-2">
							<Folder className="h-4 w-4 text-base-content/60" />
							<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
								Custom Channels
							</h4>
						</div>
						<p className="text-base-content/60 text-sm">
							Custom channels appear before auto-generated channels in Tube TV.
						</p>
					</div>
					<button type="button" className="btn btn-outline btn-sm" onClick={addChannel}>
						<Plus className="h-4 w-4" />
						Add Channel
					</button>
				</div>

				<div className="space-y-4">
					{formData.custom_channels.length === 0 && (
						<div className="rounded-xl border border-base-300 bg-base-100/70 p-4 text-base-content/60 text-sm">
							No custom channels configured.
						</div>
					)}
					{formData.custom_channels.map((channel, channelIndex) => (
						<div
							key={`tube-tv-channel-${channelIndex}`}
							className="rounded-xl border border-base-300 bg-base-100/70 p-4"
						>
							<div className="grid gap-3 md:grid-cols-[1fr_1fr_14rem_auto] md:items-end">
								<label className="form-control">
									<span className="label-text font-bold text-base-content text-sm">
										Channel Name
									</span>
									<input
										type="text"
										className="input input-bordered mt-2"
										value={channel.title}
										onChange={(event) => updateChannel(channelIndex, { title: event.target.value })}
									/>
								</label>
								<label className="form-control">
									<span className="label-text font-bold text-base-content text-sm">Channel ID</span>
									<input
										type="text"
										className="input input-bordered mt-2"
										value={channel.id}
										onChange={(event) => updateChannel(channelIndex, { id: event.target.value })}
									/>
								</label>
								<label className="form-control">
									<span className="label-text font-bold text-base-content text-sm">
										Commercials
									</span>
									<select
										className="select select-bordered mt-2"
										value={channel.commercial_category || ""}
										onChange={(event) =>
											updateChannel(channelIndex, {
												commercial_category: event.target.value,
											})
										}
									>
										<option value="">Use selected categories</option>
										{(library?.categories ?? []).map((category) => (
											<option key={category.id} value={category.id}>
												{category.title}
											</option>
										))}
									</select>
								</label>
								<button
									type="button"
									className="btn btn-error btn-outline"
									onClick={() => removeChannel(channelIndex)}
								>
									<Trash2 className="h-4 w-4" />
									Remove
								</button>
							</div>

							<div className="mt-5 space-y-4">
								<div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
									<div>
										<div className="font-bold text-base-content/50 text-xs uppercase tracking-widest">
											Channel Sources
										</div>
										<p className="mt-1 text-base-content/60 text-xs">
											Add whole libraries, movie groups, series, seasons, episodes, or single
											movies.
										</p>
									</div>
									<button
										type="button"
										className="btn btn-outline btn-sm"
										onClick={() => openBrowser(channelIndex)}
										disabled={isReadOnly}
									>
										<Search className="h-4 w-4" />
										Browse Local Library
									</button>
								</div>

								{channel.sources.length === 0 ? (
									<div className="rounded-xl border border-base-300 border-dashed bg-base-200/50 p-4 text-base-content/60 text-sm">
										No sources yet. Browse the local library to build this channel.
									</div>
								) : (
									<div className="grid gap-2 md:grid-cols-2">
										{channel.sources.map((source, sourceIndex) => (
											<div
												key={`tube-tv-source-${channelIndex}-${sourceIndex}`}
												className="flex min-w-0 items-center gap-3 rounded-lg border border-base-300 bg-base-200/70 p-3"
											>
												<div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-lg bg-primary/15 text-primary">
													{source.media_type === "movie" ? (
														<Film className="h-5 w-5" />
													) : source.media_type === "episode" || source.media_type === "show" ? (
														<Tv className="h-5 w-5" />
													) : source.category_id.startsWith("local-discover:") ? (
														<Layers className="h-5 w-5" />
													) : (
														<Folder className="h-5 w-5" />
													)}
												</div>
												<div className="min-w-0 flex-1">
													<div className="truncate font-bold text-sm">
														{sourceLabel(source, localCategories)}
													</div>
													<div className="truncate text-base-content/50 text-xs">
														{source.path || source.category_id}
													</div>
												</div>
												<button
													type="button"
													className="btn btn-error btn-outline btn-xs"
													onClick={() => removeSource(channelIndex, sourceIndex)}
													disabled={isReadOnly}
												>
													<Trash2 className="h-3 w-3" />
												</button>
											</div>
										))}
									</div>
								)}

								{browserChannelIndex === channelIndex && (
									<div className="rounded-xl border border-primary/30 bg-base-200/80 p-4">
										<div className="flex flex-col gap-3 md:flex-row md:items-start md:justify-between">
											<div className="min-w-0">
												<div className="font-bold text-primary text-xs uppercase tracking-widest">
													Local Library Browser
												</div>
												<h5 className="mt-1 truncate font-black text-xl">
													{browserData?.title || "Local Library"}
												</h5>
												<p className="mt-1 text-base-content/55 text-xs">
													Select an item to add it, or browse folders to pick a narrower channel
													source.
												</p>
											</div>
											<div className="flex flex-wrap gap-2">
												<button
													type="button"
													className="btn btn-ghost btn-sm"
													onClick={goBrowserBack}
													disabled={!browserRequest.categoryId}
												>
													<ArrowLeft className="h-4 w-4" />
													Back
												</button>
												{browserData?.source && (
													<button
														type="button"
														className="btn btn-primary btn-sm"
														onClick={() => addCurrentBrowserView(channelIndex)}
														disabled={isReadOnly}
													>
														<Check className="h-4 w-4" />
														Add This View
													</button>
												)}
												<button
													type="button"
													className="btn btn-ghost btn-sm"
													onClick={closeBrowser}
												>
													Close
												</button>
											</div>
										</div>

										<label className="input input-bordered mt-4 flex items-center gap-2">
											<Search className="h-4 w-4 text-base-content/45" />
											<input
												type="search"
												className="grow"
												value={browserSearch}
												onChange={(event) => setBrowserSearch(event.target.value)}
												placeholder="Filter this view"
											/>
										</label>

										{isBrowserLoading ? (
											<div className="mt-4 flex items-center gap-2 rounded-lg border border-base-300 bg-base-100/70 p-4 text-base-content/60 text-sm">
												<span className="loading loading-spinner loading-sm" />
												Loading library
											</div>
										) : browserRows.length === 0 ? (
											<div className="mt-4 rounded-lg border border-base-300 bg-base-100/70 p-4 text-base-content/60 text-sm">
												No matching local media found.
											</div>
										) : (
											<div className="mt-4 grid gap-2 lg:grid-cols-2">
												{browserRows.map((row) => (
													<div
														key={`${row.id || row.categoryId || row.title}-${row.sourceIndex}-${row.path || ""}`}
														className="flex min-w-0 items-center gap-3 rounded-lg border border-base-300 bg-base-100/80 p-3"
													>
														<div className="flex h-11 w-11 shrink-0 items-center justify-center rounded-lg bg-base-300/70 text-base-content/70">
															{libraryRowIcon(row)}
														</div>
														<div className="min-w-0 flex-1">
															<div className="truncate font-bold text-sm">{row.title}</div>
															<div className="truncate text-base-content/50 text-xs">
																{row.detail || row.path || row.mediaType || "Local media"}
															</div>
														</div>
														<div className="flex shrink-0 gap-2">
															{row.browsable && (
																<button
																	type="button"
																	className="btn btn-ghost btn-xs"
																	onClick={() => browseRow(row)}
																>
																	Open
																</button>
															)}
															{row.selectable && (
																<button
																	type="button"
																	className="btn btn-primary btn-xs"
																	onClick={() =>
																		addSourceToChannel(channelIndex, sourceFromRow(row))
																	}
																	disabled={isReadOnly}
																>
																	Add
																</button>
															)}
														</div>
													</div>
												))}
											</div>
										)}
									</div>
								)}
							</div>
						</div>
					))}
				</div>
			</div>
		</div>
	);
}
