import {
	Check,
	Cpu,
	Film,
	Gauge,
	Info,
	Power,
	Save,
	ScanLine,
	ShieldCheck,
	Sparkles,
	Zap,
} from "lucide-react";
import { useEffect, useState } from "react";
import type { ConfigResponse, UpscalingConfig } from "../../types/config";

interface UpscalingConfigSectionProps {
	config: ConfigResponse;
	onUpdate?: (section: string, data: UpscalingConfig) => Promise<void>;
	isReadOnly?: boolean;
	isUpdating?: boolean;
}

const DEFAULT_AI_MODEL = "fsrcnnx-8";

const UPSCALING_OPTIONS = [
	{
		mode: "auto",
		label: "Auto AI",
		badge: "Smart fallback",
		description:
			"Uses your selected AI model when the server passes its compatibility check, then steps down safely when needed.",
		icon: Gauge,
	},
	{
		mode: "ai",
		label: "AI Upscaling",
		badge: "Experimental",
		description:
			"Prioritizes the selected neural upscaler and keeps playback reliable with lighter AI and Standard fallbacks.",
		icon: Sparkles,
	},
	{
		mode: "standard",
		label: "Standard",
		badge: "Dependable",
		description:
			"Uses the fast Spline36 scaler supported across Tater Tube servers without a neural model.",
		icon: ScanLine,
	},
	{
		mode: "off",
		label: "Off",
		badge: "Original resolution",
		description:
			"Leaves the original resolution untouched so the player, television, or receiver handles enlargement.",
		icon: Power,
	},
] as const;

const AI_MODEL_OPTIONS = [
	{
		id: "fsrcnnx-8",
		family: "FSRCNNX",
		name: "Fast",
		model: "8-feature",
		badge: "Default",
		description:
			"Responsive general-purpose detail recovery with the lightest GPU load and widest compatibility.",
		tags: ["General video", "Light GPU", "2×"],
		icon: Zap,
	},
	{
		id: "fsrcnnx-16",
		family: "FSRCNNX",
		name: "Quality",
		model: "16-feature",
		badge: "Enhanced",
		description:
			"A larger general-purpose network for finer edges and textures when the GPU has more room.",
		tags: ["General video", "Medium GPU", "2×"],
		icon: Sparkles,
	},
	{
		id: "artcnn-c4f16",
		family: "ArtCNN",
		name: "Balanced",
		model: "C4F16",
		badge: "Animation",
		description:
			"A modern real-time model tuned for animation, line art, and clean computer-generated imagery.",
		tags: ["Animation", "Medium GPU", "2×"],
		icon: Film,
	},
	{
		id: "artcnn-c4f32",
		family: "ArtCNN",
		name: "Quality",
		model: "C4F32",
		badge: "Maximum detail",
		description:
			"The heavier ArtCNN option for the cleanest animated lines and detail on faster GPUs.",
		tags: ["Animation", "High GPU", "2×"],
		icon: Cpu,
	},
] as const;

const MODEL_FALLBACKS: Record<string, string> = {
	"fsrcnnx-8": "Standard",
	"fsrcnnx-16": "FSRCNNX Fast → Standard",
	"artcnn-c4f16": "FSRCNNX Fast → Standard",
	"artcnn-c4f32": "ArtCNN Balanced → FSRCNNX Fast → Standard",
};

function normalizedUpscalingConfig(value: UpscalingConfig): UpscalingConfig {
	return {
		mode: value.mode || "standard",
		model: value.model || DEFAULT_AI_MODEL,
	};
}

export function UpscalingConfigSection({
	config,
	onUpdate,
	isReadOnly = false,
	isUpdating = false,
}: UpscalingConfigSectionProps) {
	const [upscalingData, setUpscalingData] = useState<UpscalingConfig>(() =>
		normalizedUpscalingConfig(config.upscaling),
	);
	const [hasChanges, setHasChanges] = useState(false);

	useEffect(() => {
		setUpscalingData(normalizedUpscalingConfig(config.upscaling));
		setHasChanges(false);
	}, [config.upscaling]);

	const updateData = (next: UpscalingConfig) => {
		const normalized = normalizedUpscalingConfig(next);
		setUpscalingData(normalized);
		setHasChanges(
			JSON.stringify(normalized) !== JSON.stringify(normalizedUpscalingConfig(config.upscaling)),
		);
	};

	const handleSave = async () => {
		if (!onUpdate || !hasChanges) return;
		await onUpdate("upscaling", upscalingData);
		setHasChanges(false);
	};

	const aiEnabled = upscalingData.mode === "auto" || upscalingData.mode === "ai";
	const selectedModel =
		AI_MODEL_OPTIONS.find((option) => option.id === upscalingData.model) || AI_MODEL_OPTIONS[0];

	return (
		<div className="space-y-8">
			<div className="relative overflow-hidden rounded-3xl border border-primary/15 bg-gradient-to-br from-primary/10 via-base-200 to-secondary/10 p-6 shadow-sm">
				<div className="-right-16 -top-20 pointer-events-none absolute h-48 w-48 rounded-full bg-secondary/10 blur-3xl" />
				<div className="-bottom-24 -left-12 pointer-events-none absolute h-48 w-48 rounded-full bg-primary/10 blur-3xl" />
				<div className="relative flex items-start gap-4">
					<div className="flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl bg-primary text-primary-content shadow-lg shadow-primary/20">
						<Sparkles className="h-6 w-6" />
					</div>
					<div>
						<div className="flex flex-wrap items-center gap-2">
							<h3 className="font-bold text-base-content text-xl">Automatic TV Upscaling</h3>
							<span className="badge badge-secondary badge-sm font-semibold">GPU accelerated</span>
						</div>
						<p className="mt-1 max-w-3xl text-base-content/60 text-sm leading-relaxed">
							Choose how Tater Tube enlarges lower-resolution video before it reaches a
							higher-resolution television. Matching-resolution media is always left untouched.
						</p>
					</div>
				</div>
			</div>

			<div className="grid gap-4 md:grid-cols-2">
				{UPSCALING_OPTIONS.map((option) => {
					const selected = (upscalingData.mode || "standard") === option.mode;
					const Icon = option.icon;
					return (
						<button
							type="button"
							key={option.mode}
							className={`group relative flex gap-4 rounded-2xl border-2 p-5 text-left transition-all ${
								selected
									? "border-primary bg-primary/10 shadow-md shadow-primary/10"
									: "hover:-translate-y-0.5 border-base-300/80 bg-base-200/60 hover:border-primary/40 hover:shadow-sm"
							} ${isReadOnly ? "cursor-default opacity-70" : ""}`}
							disabled={isReadOnly}
							onClick={() => updateData({ ...upscalingData, mode: option.mode })}
						>
							<div
								className={`mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl transition-colors ${
									selected
										? "bg-primary text-primary-content"
										: "bg-base-300/70 text-base-content/60"
								}`}
							>
								<Icon className="h-4.5 w-4.5" />
							</div>
							<div className="min-w-0 flex-1">
								<div className="flex flex-wrap items-center gap-2">
									<span className="font-bold text-base-content">{option.label}</span>
									<span className={`badge badge-sm ${selected ? "badge-primary" : "badge-ghost"}`}>
										{option.badge}
									</span>
								</div>
								<p className="mt-2 text-[11px] text-base-content/55 leading-relaxed">
									{option.description}
								</p>
							</div>
							{selected && (
								<span className="absolute top-4 right-4 flex h-5 w-5 items-center justify-center rounded-full bg-primary text-primary-content">
									<Check className="h-3.5 w-3.5" />
								</span>
							)}
						</button>
					);
				})}
			</div>

			{aiEnabled && (
				<div className="overflow-hidden rounded-3xl border border-secondary/20 bg-base-200/55 shadow-sm">
					<div className="flex flex-col gap-3 border-base-300/70 border-b bg-gradient-to-r from-secondary/10 to-transparent px-6 py-5 sm:flex-row sm:items-center sm:justify-between">
						<div>
							<div className="flex items-center gap-2">
								<Sparkles className="h-4.5 w-4.5 text-secondary" />
								<h4 className="font-bold text-base-content">Choose an AI model</h4>
							</div>
							<p className="mt-1 text-base-content/55 text-xs">
								All models run locally through the GPU. Nothing is uploaded or analyzed remotely.
							</p>
						</div>
						<div className="flex items-center gap-2 rounded-full border border-success/20 bg-success/10 px-3 py-1.5 text-success text-xs">
							<ShieldCheck className="h-3.5 w-3.5" />
							Automatic fallback
						</div>
					</div>

					<div className="grid gap-3 p-4 lg:grid-cols-2">
						{AI_MODEL_OPTIONS.map((option) => {
							const selected = selectedModel.id === option.id;
							const Icon = option.icon;
							return (
								<button
									type="button"
									key={option.id}
									className={`relative rounded-2xl border p-4 text-left transition-all ${
										selected
											? "border-secondary/60 bg-secondary/10 shadow-md shadow-secondary/5"
											: "hover:-translate-y-0.5 border-base-300 bg-base-100/70 hover:border-secondary/35 hover:bg-base-100"
									} ${isReadOnly ? "cursor-default opacity-70" : ""}`}
									disabled={isReadOnly}
									onClick={() => updateData({ ...upscalingData, model: option.id })}
								>
									<div className="flex items-start gap-3">
										<div
											className={`flex h-10 w-10 shrink-0 items-center justify-center rounded-xl ${
												selected
													? "bg-secondary text-secondary-content"
													: "bg-base-200 text-base-content/55"
											}`}
										>
											<Icon className="h-5 w-5" />
										</div>
										<div className="min-w-0 flex-1">
											<div className="flex flex-wrap items-center gap-x-2 gap-y-1 pr-6">
												<span className="font-bold text-base-content">
													{option.family} {option.name}
												</span>
												<span className="font-mono text-[10px] text-base-content/40">
													{option.model}
												</span>
											</div>
											<span className="mt-1 inline-flex rounded-full bg-secondary/10 px-2 py-0.5 font-semibold text-[9px] text-secondary uppercase tracking-wider">
												{option.badge}
											</span>
										</div>
									</div>
									<p className="mt-3 text-[11px] text-base-content/55 leading-relaxed">
										{option.description}
									</p>
									<div className="mt-3 flex flex-wrap gap-1.5">
										{option.tags.map((tag) => (
											<span
												key={tag}
												className="rounded-full border border-base-300 bg-base-200/70 px-2 py-0.5 text-[9px] text-base-content/55"
											>
												{tag}
											</span>
										))}
									</div>
									{selected && (
										<span className="absolute top-3 right-3 flex h-5 w-5 items-center justify-center rounded-full bg-secondary text-secondary-content">
											<Check className="h-3.5 w-3.5" />
										</span>
									)}
								</button>
							);
						})}
					</div>

					<div className="flex flex-col gap-2 border-base-300/70 border-t bg-base-100/55 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
						<div className="text-xs">
							<span className="text-base-content/45">Selected model</span>
							<span className="ml-2 font-bold text-base-content">
								{selectedModel.family} {selectedModel.name}
							</span>
						</div>
						<div className="text-[10px] text-base-content/45">
							Fallback: {MODEL_FALLBACKS[selectedModel.id]}
						</div>
					</div>
				</div>
			)}

			<div className="alert items-start rounded-2xl border border-info/20 bg-info/5 p-4 shadow-sm">
				<Info className="mt-0.5 h-5 w-5 shrink-0 text-info" />
				<div className="min-w-0 flex-1">
					<div className="font-bold text-info text-xs uppercase tracking-wider">When it runs</div>
					<div className="mt-1 text-[11px] leading-relaxed opacity-80">
						AI targets compatible SDR upscales between 1.3× and 2×. HDR, unusually large source
						frames, unsupported GPU paths, and failed model checks automatically use Standard
						instead. ArtCNN is designed for animation and line art; FSRCNNX is the safer choice for
						general video.
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
