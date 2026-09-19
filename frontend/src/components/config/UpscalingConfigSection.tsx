import { Gauge, Info, Power, Save, ScanLine, Sparkles } from "lucide-react";
import { useEffect, useState } from "react";
import type { ConfigResponse, UpscalingConfig } from "../../types/config";

interface UpscalingConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: UpscalingConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const UPSCALING_OPTIONS = [
	{
		mode: "auto",
		label: "Auto AI",
		badge: "AI",
		description:
			"Uses AI upscaling when the server passes its compatibility check, with an automatic Standard fallback.",
		icon: Gauge,
	},
	{
		mode: "ai",
		label: "AI Upscaling",
		badge: "AI · Experimental",
		description:
			"Prefers the fast FSRCNNX AI model. Unsupported hardware or FFmpeg builds safely fall back to Standard.",
		icon: Sparkles,
	},
	{
		mode: "standard",
		label: "Standard",
		badge: "Recommended",
		description:
			"Uses the fast and dependable Spline36 scaler already supported across Tater Tube servers.",
		icon: ScanLine,
	},
	{
		mode: "off",
		label: "Off",
		badge: "Original resolution",
		description:
			"Does not upscale on the server. The player, television, or receiver handles enlargement instead.",
		icon: Power,
	},
] as const;

export function UpscalingConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: UpscalingConfigSectionProps) {
	const [upscalingData, setUpscalingData] = useState<UpscalingConfig>(config.upscaling);
	const [hasChanges, setHasChanges] = useState(false);

	useEffect(() => {
		setUpscalingData(config.upscaling);
		setHasChanges(false);
	}, [config.upscaling]);

	const selectMode = (mode: string) => {
		const next = { ...upscalingData, mode };
		setUpscalingData(next);
		setHasChanges(JSON.stringify(next) !== JSON.stringify(config.upscaling));
	};

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("upscaling", upscalingData);
		setHasChanges(false);
	};

	return (
		<div className="space-y-10">
			<div>
				<h3 className="font-bold text-base-content text-lg">Automatic TV Upscaling</h3>
				<p className="text-base-content/50 text-sm">
					Choose the server-side method used when the television resolution is higher than the video
					resolution.
				</p>
			</div>

			<div className="grid gap-4 md:grid-cols-2">
				{UPSCALING_OPTIONS.map((option) => {
					const selected = (upscalingData.mode || "standard") === option.mode;
					const Icon = option.icon;
					return (
						<label
							key={option.mode}
							className={`relative flex cursor-pointer gap-4 rounded-2xl border-2 p-5 transition-all ${
								selected
									? "border-primary bg-primary/10 shadow-md shadow-primary/10"
									: "border-base-300/80 bg-base-200/60 hover:border-primary/40"
							} ${isReadOnly ? "cursor-default opacity-70" : ""}`}
						>
							<input
								type="radio"
								name="upscaling-mode"
								className="radio radio-primary mt-1"
								checked={selected}
								disabled={isReadOnly}
								onChange={() => selectMode(option.mode)}
							/>
							<div className="min-w-0 flex-1">
								<div className="flex flex-wrap items-center gap-2">
									<Icon className="h-4 w-4 text-primary" />
									<span className="font-bold text-base-content">{option.label}</span>
									<span
										className={`badge badge-sm ${option.badge.startsWith("AI") ? "badge-secondary" : "badge-ghost"}`}
									>
										{option.badge}
									</span>
								</div>
								<p className="mt-2 text-[11px] text-base-content/55 leading-relaxed">
									{option.description}
								</p>
							</div>
						</label>
					);
				})}
			</div>

			<div className="alert items-start rounded-2xl border border-info/20 bg-info/5 p-4 shadow-sm">
				<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
				<div className="min-w-0 flex-1">
					<div className="font-bold text-info text-xs uppercase tracking-wider">When it runs</div>
					<div className="mt-1 text-[11px] leading-relaxed opacity-80">
						Tater Tube compares the media resolution with the connected TV. This setting is only
						used when the TV is higher resolution; matching-resolution and higher-resolution media
						are left alone. AI currently targets compatible SDR upscales up to 2× and uses Standard
						for everything else.
					</div>
				</div>
			</div>

			{!isReadOnly && (
				<div className="flex justify-end border-base-200 border-t pt-4">
					<button
						type="button"
						className={`btn btn-primary px-10 shadow-lg shadow-primary/20 ${!hasChanges && "btn-ghost border-base-300"}`}
						onClick={handleSave}
						disabled={!hasChanges || isUpdating}
					>
						{isUpdating ? (
							<span className="loading loading-spinner loading-sm" />
						) : (
							<Save className="h-4 w-4" />
						)}
						{isUpdating ? "Saving..." : "Save Changes"}
					</button>
				</div>
			)}
		</div>
	);
}
