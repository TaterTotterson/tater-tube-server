import { Download, Film, Info, Radio, Save, Tv } from "lucide-react";
import { useEffect, useState } from "react";
import type { ConfigResponse, TaterBumpersConfig } from "../../types/config";

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
	const [formData, setFormData] = useState<TaterBumpersConfig>(() => formFromConfig(config));
	const [hasChanges, setHasChanges] = useState(false);

	useEffect(() => {
		setFormData(formFromConfig(config));
		setHasChanges(false);
	}, [config]);

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

	const options = [
		{
			key: "live_tv" as const,
			title: "Live TV",
			description: "Include a built-in Tater bumper in server-planned Tube TV breaks.",
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
			description: "Play one occasionally between server-local episodes.",
			icon: Tv,
		},
		{
			key: "nzb_movies" as const,
			title: "NZB Movies",
			description: "Play one while an NZB movie stream is preparing and buffering.",
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
							Choose where the server allows its paired Tater Tube players to use the built-in Tater
							bumpers.
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

			<div className="alert items-start rounded-2xl border border-info/20 bg-info/5 p-4 shadow-sm">
				<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
				<div className="min-w-0 flex-1">
					<div className="font-bold text-info text-xs uppercase tracking-wider">
						Player Controls
					</div>
					<div className="mt-1 break-words text-[11px] leading-relaxed opacity-80">
						Each player also has its own Tater Bumpers settings. Server-backed playback only uses a
						bumper when both the server policy and that player&apos;s setting are enabled.
					</div>
				</div>
			</div>
		</div>
	);
}
