import { Folder, Plus, Save, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import type { ConfigResponse, LocalMediaCategory, LocalMediaConfig } from "../../types/config";

interface LocalMediaConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: LocalMediaConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const DEFAULT_LOCAL_MEDIA: LocalMediaConfig = {
	enabled: false,
	categories: [],
};

function slug(value: string) {
	return value
		.toLowerCase()
		.trim()
		.replace(/[^a-z0-9]+/g, "-")
		.replace(/^-+|-+$/g, "")
		.slice(0, 64);
}

function normalize(config: ConfigResponse): LocalMediaConfig {
	const source = config.local_media ?? DEFAULT_LOCAL_MEDIA;
	return {
		enabled: source.enabled ?? false,
		categories: (source.categories ?? []).map((category) => ({
			id: category.id || slug(category.name || "local"),
			name: category.name || "Local",
			library_type: category.library_type || "movies",
			paths: category.paths ?? [],
			enabled: category.enabled ?? true,
		})),
	};
}

export function LocalMediaConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: LocalMediaConfigSectionProps) {
	const [formData, setFormData] = useState<LocalMediaConfig>(() => normalize(config));
	const [hasChanges, setHasChanges] = useState(false);

	useEffect(() => {
		setFormData(normalize(config));
		setHasChanges(false);
	}, [config]);

	const update = (next: LocalMediaConfig) => {
		setFormData(next);
		setHasChanges(JSON.stringify(next) !== JSON.stringify(normalize(config)));
	};

	const updateCategory = (index: number, patch: Partial<LocalMediaCategory>) => {
		const categories = formData.categories.map((category, i) => {
			if (i !== index) return category;
			const next = { ...category, ...patch };
			if (patch.name !== undefined && (!next.id || next.id === slug(category.name))) {
				next.id = slug(patch.name) || next.id;
			}
			return next;
		});
		update({ ...formData, categories });
	};

	const addCategory = () => {
		const count = formData.categories.length + 1;
		update({
			...formData,
			categories: formData.categories.concat([
				{
					id: `local-${count}`,
					name: `Local ${count}`,
					library_type: "movies",
					paths: [""],
					enabled: true,
				},
			]),
		});
	};

	const removeCategory = (index: number) => {
		update({ ...formData, categories: formData.categories.filter((_, i) => i !== index) });
	};

	const addPath = (index: number) => {
		const category = formData.categories[index];
		updateCategory(index, { paths: (category.paths ?? []).concat([""]) });
	};

	const updatePath = (categoryIndex: number, pathIndex: number, value: string) => {
		const category = formData.categories[categoryIndex];
		const paths = (category.paths ?? []).map((path, i) => (i === pathIndex ? value : path));
		updateCategory(categoryIndex, { paths });
	};

	const removePath = (categoryIndex: number, pathIndex: number) => {
		const category = formData.categories[categoryIndex];
		const paths = (category.paths ?? []).filter((_, i) => i !== pathIndex);
		updateCategory(categoryIndex, { paths: paths.length > 0 ? paths : [""] });
	};

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("local_media", {
			enabled: formData.enabled,
			categories: formData.categories.map((category) => ({
				...category,
				id: slug(category.id || category.name),
				name: category.name.trim(),
				library_type: category.library_type || "movies",
				paths: (category.paths ?? []).map((path) => path.trim()).filter(Boolean),
				enabled: category.enabled ?? true,
			})),
		});
		setHasChanges(false);
	};

	return (
		<div className="min-w-0 space-y-8">
			<div className="rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
				<div className="mb-6 flex flex-col gap-4 sm:flex-row sm:items-start sm:justify-between">
					<div className="min-w-0">
						<div className="mb-3 flex items-center gap-2">
							<Folder className="h-4 w-4 text-base-content/60" />
							<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
								Local Media
							</h4>
						</div>
						<p className="max-w-2xl text-base-content/60 text-sm leading-relaxed">
							Add server or container folder paths. Movies, TV, and Folders appear under The Tube
							Local. Music appears in Tape Deck when Tater Tube Server is selected.
						</p>
					</div>
					<label className="flex items-center gap-3">
						<span className="font-bold text-sm">Enabled</span>
						<input
							type="checkbox"
							className="toggle toggle-primary"
							checked={formData.enabled}
							disabled={isReadOnly}
							onChange={(event) => update({ ...formData, enabled: event.target.checked })}
						/>
					</label>
				</div>

				<div className="space-y-4">
					{formData.categories.length === 0 && (
						<div className="rounded-xl border border-base-300 bg-base-100/70 p-4 text-base-content/60 text-sm">
							No local categories configured.
						</div>
					)}
					{formData.categories.map((category, categoryIndex) => (
						<div
							key={`local-category-${categoryIndex}`}
							className="rounded-xl border border-base-300 bg-base-100/70 p-4"
						>
							<div className="grid gap-3 md:grid-cols-[1fr_1fr_12rem_auto] md:items-end">
								<label className="form-control">
									<span className="label-text font-bold text-base-content text-sm">
										Category Name
									</span>
									<input
										type="text"
										className="input input-bordered mt-2 w-full"
										value={category.name}
										disabled={isReadOnly}
										onChange={(event) =>
											updateCategory(categoryIndex, { name: event.target.value })
										}
									/>
								</label>
								<label className="form-control">
									<span className="label-text font-bold text-base-content text-sm">
										Category ID
									</span>
									<input
										type="text"
										className="input input-bordered mt-2 w-full"
										value={category.id}
										disabled={isReadOnly}
										onChange={(event) => updateCategory(categoryIndex, { id: event.target.value })}
									/>
								</label>
								<label className="form-control">
									<span className="label-text font-bold text-base-content text-sm">
										Library Type
									</span>
									<select
										className="select select-bordered mt-2 w-full"
										value={category.library_type || "movies"}
										disabled={isReadOnly}
										onChange={(event) =>
											updateCategory(categoryIndex, { library_type: event.target.value })
										}
									>
										<option value="movies">Movies</option>
										<option value="tv">TV Shows</option>
										<option value="music">Music</option>
										<option value="folders">Folders</option>
									</select>
								</label>
								<button
									type="button"
									className="btn btn-error btn-outline"
									disabled={isReadOnly}
									onClick={() => removeCategory(categoryIndex)}
								>
									<Trash2 className="h-4 w-4" />
									Remove
								</button>
							</div>

							<div className="mt-4 space-y-2">
								<div className="font-bold text-base-content/50 text-xs uppercase tracking-widest">
									Folders
								</div>
								{((category.paths ?? []).length > 0 ? category.paths : [""]).map(
									(path, pathIndex) => (
										<div
											key={`local-category-${categoryIndex}-path-${pathIndex}`}
											className="flex gap-2"
										>
											<input
												type="text"
												className="input input-bordered w-full"
												placeholder="/media/movies"
												value={path}
												disabled={isReadOnly}
												onChange={(event) =>
													updatePath(categoryIndex, pathIndex, event.target.value)
												}
											/>
											<button
												type="button"
												className="btn btn-ghost btn-square"
												disabled={isReadOnly}
												onClick={() => removePath(categoryIndex, pathIndex)}
												aria-label="Remove folder"
											>
												<Trash2 className="h-4 w-4" />
											</button>
										</div>
									),
								)}
								<button
									type="button"
									className="btn btn-outline btn-sm"
									disabled={isReadOnly}
									onClick={() => addPath(categoryIndex)}
								>
									<Plus className="h-4 w-4" />
									Add Folder
								</button>
							</div>
						</div>
					))}
				</div>

				<div className="mt-6">
					<button
						type="button"
						className="btn btn-outline rounded-full"
						disabled={isReadOnly}
						onClick={addCategory}
					>
						<Plus className="h-4 w-4" />
						Add Category
					</button>
				</div>
			</div>

			{!isReadOnly && (
				<div className="flex justify-end border-base-200 border-t pt-6">
					<button
						type="button"
						className="btn btn-primary rounded-full px-8"
						onClick={handleSave}
						disabled={!hasChanges || isUpdating}
					>
						{isUpdating ? (
							<span className="loading loading-spinner loading-sm" />
						) : (
							<Save className="h-4 w-4" />
						)}
						Save Local Media
					</button>
				</div>
			)}
		</div>
	);
}
