import { Clapperboard, Folder, Plus, RefreshCw, Save, Trash2, Upload } from "lucide-react";
import { useEffect, useState } from "react";
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
} from "../../types/config";

interface TubeTVConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: TubeTVConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const DEFAULT_TUBE_TV: TubeTVConfig = {
	auto_channels: true,
	commercials_enabled: true,
	midroll_commercials: false,
	commercial_categories: [],
	commercials_path: "",
	custom_channels: [],
};

function slug(value: string) {
	return value
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 64);
}

function normalize(config: ConfigResponse): TubeTVConfig {
	const source = config.tube_tv ?? DEFAULT_TUBE_TV;
	return {
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

function emptySource(categories: LocalMediaCategory[]): TubeTVCustomSource {
	return {
		category_id: categories[0]?.id || "",
		source_index: -1,
		path: "",
		title: "",
		media_type: "",
	};
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
	const [hasChanges, setHasChanges] = useState(false);

	const localCategories = (config.local_media?.categories ?? []).filter(
		(category) => category.enabled !== false && category.library_type !== "music",
	);

	useEffect(() => {
		setFormData(normalize(config));
		setHasChanges(false);
	}, [config]);

	const refreshLibrary = async () => {
		setIsLibraryLoading(true);
		try {
			const data = await apiClient.getTubeTVCommercials();
			setLibrary(data);
			if (!uploadCategory && data.categories.length > 0) {
				setUploadCategory(data.categories[0].id);
			}
		} catch (error) {
			showToast({
				type: "error",
				title: "Commercials Failed",
				message: error instanceof Error ? error.message : "Unable to load commercials.",
			});
		} finally {
			setIsLibraryLoading(false);
		}
	};

	useEffect(() => {
		void refreshLibrary();
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, []);

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

	const updateSource = (
		channelIndex: number,
		sourceIndex: number,
		patch: Partial<TubeTVCustomSource>,
	) => {
		const channel = formData.custom_channels[channelIndex];
		const sources = channel.sources.map((source, i) =>
			i === sourceIndex ? { ...source, ...patch } : source,
		);
		updateChannel(channelIndex, { sources });
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
					sources: [emptySource(localCategories)],
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

	const addSource = (channelIndex: number) => {
		const channel = formData.custom_channels[channelIndex];
		updateChannel(channelIndex, { sources: channel.sources.concat([emptySource(localCategories)]) });
	};

	const removeSource = (channelIndex: number, sourceIndex: number) => {
		const channel = formData.custom_channels[channelIndex];
		const sources = channel.sources.filter((_, i) => i !== sourceIndex);
		updateChannel(channelIndex, { sources });
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
							category_id: slug(source.category_id),
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

	return (
		<div className="min-w-0 space-y-8">
			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
					<div className="min-w-0">
						<div className="mb-3 flex items-center gap-2">
							<Clapperboard className="h-4 w-4 text-base-content/60" />
							<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
								Server TV Mode
							</h4>
						</div>
						<p className="max-w-2xl text-base-content/60 text-sm leading-relaxed">
							Build The Tube TV Mode on the server so every paired Tater Tube player receives
							the same channels, commercials, and custom lineups.
						</p>
					</div>
					<button
						type="button"
						className="btn btn-primary"
						disabled={!hasChanges || isUpdating || isReadOnly}
						onClick={save}
					>
						{isUpdating ? <span className="loading loading-spinner loading-xs" /> : <Save className="h-4 w-4" />}
						Save
					</button>
				</div>

				<div className="grid gap-4 md:grid-cols-3">
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
							Upload local commercial videos and pick which categories TV Mode can use.
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
					<label className={`btn btn-primary ${!uploadCategory || isUploading ? "btn-disabled" : ""}`}>
						{isUploading ? <span className="loading loading-spinner loading-xs" /> : <Upload className="h-4 w-4" />}
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
							Custom channels appear before auto-generated channels in The Tube TV Mode.
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
										onChange={(event) =>
											updateChannel(channelIndex, { title: event.target.value })
										}
									/>
								</label>
								<label className="form-control">
									<span className="label-text font-bold text-base-content text-sm">
										Channel ID
									</span>
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

							<div className="mt-4 space-y-3">
								<div className="flex items-center justify-between">
									<div className="font-bold text-base-content/50 text-xs uppercase tracking-widest">
										Sources
									</div>
									<button
										type="button"
										className="btn btn-ghost btn-xs"
										onClick={() => addSource(channelIndex)}
									>
										<Plus className="h-3 w-3" />
										Add Source
									</button>
								</div>

								{channel.sources.map((source, sourceIndex) => (
									<div
										key={`tube-tv-source-${channelIndex}-${sourceIndex}`}
										className="grid gap-3 rounded-lg border border-base-300 bg-base-200/70 p-3 lg:grid-cols-[12rem_6rem_1fr_1fr_9rem_auto] lg:items-end"
									>
										<label className="form-control">
											<span className="label-text text-xs">Local Category</span>
											<select
												className="select select-bordered select-sm mt-1"
												value={source.category_id}
												onChange={(event) =>
													updateSource(channelIndex, sourceIndex, {
														category_id: event.target.value,
													})
												}
											>
												<option value="">Select</option>
												{localCategories.map((category) => (
													<option key={category.id} value={category.id}>
														{category.name}
													</option>
												))}
											</select>
										</label>
										<label className="form-control">
											<span className="label-text text-xs">Folder #</span>
											<input
												type="number"
												className="input input-bordered input-sm mt-1"
												value={source.source_index}
												onChange={(event) =>
													updateSource(channelIndex, sourceIndex, {
														source_index: Number(event.target.value),
													})
												}
											/>
										</label>
										<label className="form-control">
											<span className="label-text text-xs">Path</span>
											<input
												type="text"
												className="input input-bordered input-sm mt-1"
												value={source.path}
												placeholder="Season 1"
												onChange={(event) =>
													updateSource(channelIndex, sourceIndex, { path: event.target.value })
												}
											/>
										</label>
										<label className="form-control">
											<span className="label-text text-xs">Title Hint</span>
											<input
												type="text"
												className="input input-bordered input-sm mt-1"
												value={source.title || ""}
												onChange={(event) =>
													updateSource(channelIndex, sourceIndex, { title: event.target.value })
												}
											/>
										</label>
										<label className="form-control">
											<span className="label-text text-xs">Type</span>
											<select
												className="select select-bordered select-sm mt-1"
												value={source.media_type || ""}
												onChange={(event) =>
													updateSource(channelIndex, sourceIndex, {
														media_type: event.target.value,
													})
												}
											>
												<option value="">Auto</option>
												<option value="movie">Movie</option>
												<option value="episode">Episode</option>
												<option value="show">Series</option>
											</select>
										</label>
										<button
											type="button"
											className="btn btn-error btn-outline btn-sm"
											onClick={() => removeSource(channelIndex, sourceIndex)}
										>
											<Trash2 className="h-3 w-3" />
										</button>
									</div>
								))}
							</div>
						</div>
					))}
				</div>
			</div>
		</div>
	);
}
