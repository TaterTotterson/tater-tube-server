import { Info, Link, Save } from "lucide-react";
import { useEffect, useState } from "react";
import type { ConfigResponse, NewznabConfig } from "../../types/config";

interface NewznabConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: NewznabConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const DEFAULT_NEWZNAB: NewznabConfig = {
	enabled: false,
	url: "",
	api_key: "",
	username: "",
	browse_limit: 100,
};

function formFromConfig(config: ConfigResponse): NewznabConfig {
	const source = config.newznab ?? DEFAULT_NEWZNAB;
	return {
		enabled: source.enabled ?? false,
		url: source.url ?? "",
		api_key: "",
		api_key_set: source.api_key_set ?? false,
		username: source.username ?? "",
		browse_limit: source.browse_limit || 100,
	};
}

export function NewznabConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: NewznabConfigSectionProps) {
	const [formData, setFormData] = useState<NewznabConfig>(() => formFromConfig(config));
	const [hasChanges, setHasChanges] = useState(false);

	useEffect(() => {
		setFormData(formFromConfig(config));
		setHasChanges(false);
	}, [config]);

	const markChanged = (updated: NewznabConfig) => {
		const source = config.newznab ?? DEFAULT_NEWZNAB;
		const changed =
			updated.enabled !== (source.enabled ?? false) ||
			updated.url !== (source.url ?? "") ||
			updated.username !== (source.username ?? "") ||
			updated.browse_limit !== (source.browse_limit || 100) ||
			updated.api_key.trim() !== "";
		setHasChanges(changed);
	};

	const update = (patch: Partial<NewznabConfig>) => {
		const updated = { ...formData, ...patch };
		setFormData(updated);
		markChanged(updated);
	};

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("newznab", {
			enabled: formData.enabled,
			url: formData.url.trim(),
			api_key: formData.api_key.trim(),
			username: formData.username?.trim() ?? "",
			browse_limit: formData.browse_limit,
		});
		setHasChanges(false);
	};

	return (
		<div className="min-w-0 space-y-10">
			<div className="min-w-0 space-y-8">
				<div className="min-w-0 space-y-6 overflow-hidden rounded-2xl border-2 border-base-300/80 bg-base-200/60 p-6">
					<div className="flex items-center gap-2">
						<Link className="h-4 w-4 text-base-content/60" />
						<h4 className="font-bold text-base-content/40 text-xs uppercase tracking-widest">
							Player Stream Catalog
						</h4>
					</div>

					<div className="flex items-center justify-between gap-6">
						<div className="min-w-0">
							<h5 className="font-bold text-sm">Enable Newznab Stream</h5>
							<p className="mt-1 break-words text-[11px] text-base-content/50 leading-relaxed">
								Tater Tube players browse this server instead of talking directly to the indexer.
							</p>
						</div>
						<input
							type="checkbox"
							className="toggle toggle-primary"
							checked={formData.enabled}
							disabled={isReadOnly}
							onChange={(e) => update({ enabled: e.target.checked })}
						/>
					</div>

					<div className="grid gap-6 md:grid-cols-2">
						<label className="form-control md:col-span-2">
							<span className="label-text font-bold text-base-content text-sm">Newznab URL</span>
							<input
								type="url"
								className="input input-bordered mt-2 w-full"
								placeholder="https://indexer.example.com"
								value={formData.url}
								disabled={isReadOnly}
								onChange={(e) => update({ url: e.target.value })}
							/>
							<span className="mt-2 text-[11px] text-base-content/50">
								Base site or API URL. The server adds /api when needed.
							</span>
						</label>

						<label className="form-control">
							<span className="label-text font-bold text-base-content text-sm">API Key</span>
							<input
								type="password"
								className="input input-bordered mt-2 w-full"
								placeholder={formData.api_key_set ? "Saved - leave blank to keep" : "Newznab API key"}
								value={formData.api_key}
								disabled={isReadOnly}
								onChange={(e) => update({ api_key: e.target.value })}
							/>
						</label>

						<label className="form-control">
							<span className="label-text font-bold text-base-content text-sm">
								Trending Username
							</span>
							<input
								type="text"
								className="input input-bordered mt-2 w-full"
								placeholder="Optional"
								value={formData.username ?? ""}
								disabled={isReadOnly}
								onChange={(e) => update({ username: e.target.value })}
							/>
						</label>

						<label className="form-control">
							<span className="label-text font-bold text-base-content text-sm">Browse Limit</span>
							<select
								className="select select-bordered mt-2 w-full"
								value={formData.browse_limit}
								disabled={isReadOnly}
								onChange={(e) =>
									update({ browse_limit: Number.parseInt(e.target.value, 10) || 100 })
								}
							>
								<option value={50}>50 items</option>
								<option value={100}>100 items</option>
								<option value={250}>250 items</option>
								<option value={500}>500 items</option>
							</select>
						</label>
					</div>
				</div>

				<div className="alert items-start rounded-2xl border border-info/20 bg-info/5 p-4 shadow-sm">
					<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
					<div className="min-w-0 flex-1">
						<div className="font-bold text-info text-xs uppercase tracking-wider">
							Player Setup
						</div>
						<div className="mt-1 break-words text-[11px] leading-relaxed opacity-80">
							In Tater Tube, enter this server URL and a pairing PIN from the Tater Tube Players page.
							Indexer details stay on the server.
						</div>
					</div>
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
						Save Newznab
					</button>
				</div>
			)}
		</div>
	);
}
