import {
	Check,
	Cpu,
	Film,
	Gauge,
	Info,
	Power,
	RefreshCw,
	Save,
	ScanLine,
	ShieldCheck,
	Sparkles,
	Zap,
} from "lucide-react";
import { useEffect, useState } from "react";
import { apiClient } from "../../api/client";
import type {
	ConfigResponse,
	UpscalingCompatibilityDetection,
	UpscalingConfig,
} from "../../types/config";

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
			"Prioritizes the selected neural upscaler and keeps playback reliable with compatible AI and Standard fallbacks.",
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
		id: "anime4k-cnn-m",
		family: "Anime4K",
		name: "Balanced",
		model: "CNN M",
		badge: "Real-time animation",
		description:
			"A lightweight line-art network designed for responsive anime and animated-video playback.",
		tags: ["Animation", "Light GPU", "2×"],
		icon: Zap,
	},
	{
		id: "anime4k-cnn-l",
		family: "Anime4K",
		name: "Quality",
		model: "CNN L",
		badge: "High-end animation",
		description:
			"A larger animation network for finer line reconstruction when the GPU has extra processing headroom.",
		tags: ["Animation", "High GPU", "2×"],
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
		id: "artcnn-c4f16-ds",
		family: "ArtCNN",
		name: "Restore",
		model: "C4F16 DS",
		badge: "Denoise + sharpen",
		description:
			"Cleans compression noise while restoring crisp animated lines at the lighter C4F16 performance tier.",
		tags: ["Compressed animation", "Medium GPU", "2×"],
		icon: ShieldCheck,
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
	"anime4k-cnn-m": "FSRCNNX Fast → Standard",
	"anime4k-cnn-l": "Anime4K Balanced → FSRCNNX Fast → Standard",
	"artcnn-c4f16": "FSRCNNX Fast → Standard",
	"artcnn-c4f16-ds": "ArtCNN Balanced → FSRCNNX Fast → Standard",
	"artcnn-c4f32": "ArtCNN Balanced → FSRCNNX Fast → Standard",
};

const AI_MODEL_GROUPS = [
	{
		id: "fsrcnnx",
		label: "General video",
		description: "Natural detail for movies, live action, and everyday television.",
		modelIds: ["fsrcnnx-8", "fsrcnnx-16"],
	},
	{
		id: "anime4k",
		label: "Fast animation",
		description: "Efficient line reconstruction designed for anime and animated video.",
		modelIds: ["anime4k-cnn-m", "anime4k-cnn-l"],
	},
	{
		id: "artcnn",
		label: "Animation detail & restoration",
		description: "Specialized choices for clean line art, compressed sources, and maximum detail.",
		modelIds: ["artcnn-c4f16", "artcnn-c4f16-ds", "artcnn-c4f32"],
	},
] as const;

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
	const [compatibility, setCompatibility] = useState<UpscalingCompatibilityDetection | null>(null);
	const [isDetecting, setIsDetecting] = useState(false);
	const [detectionError, setDetectionError] = useState("");

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

	const runCompatibilityDetection = async () => {
		setIsDetecting(true);
		setDetectionError("");
		try {
			const detection = await apiClient.detectUpscalingCompatibility();
			setCompatibility(detection);
			updateData({
				...upscalingData,
				mode: detection.recommended_mode,
				model: detection.recommended_model || upscalingData.model || DEFAULT_AI_MODEL,
			});
		} catch (error) {
			setDetectionError(error instanceof Error ? error.message : "Upscaling detection failed");
		} finally {
			setIsDetecting(false);
		}
	};

	const aiEnabled = upscalingData.mode === "auto" || upscalingData.mode === "ai";
	const selectedMode =
		UPSCALING_OPTIONS.find((option) => option.mode === upscalingData.mode) || UPSCALING_OPTIONS[2];
	const selectedModel =
		AI_MODEL_OPTIONS.find((option) => option.id === upscalingData.model) || AI_MODEL_OPTIONS[0];
	const safetyPath = aiEnabled
		? MODEL_FALLBACKS[selectedModel.id]
		: upscalingData.mode === "standard"
			? "Spline36 → portable Spline"
			: "Player or display scaling";
	const compatibleModelCount = compatibility?.models.filter((model) => model.available).length || 0;
	const recommendedModel = AI_MODEL_OPTIONS.find(
		(option) => option.id === compatibility?.recommended_model,
	);
	const recommendationLabel =
		compatibility?.recommended_mode === "auto"
			? `Auto AI${recommendedModel ? ` · ${recommendedModel.family} ${recommendedModel.name}` : ""}`
			: compatibility?.recommended_mode === "standard"
				? "Standard upscaling"
				: "Upscaling off";

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

			<div className="overflow-hidden rounded-2xl border border-base-300/80 bg-base-200/45 shadow-sm">
				<div className="grid sm:grid-cols-3">
					<div className="border-base-300/70 border-b p-4 sm:border-r sm:border-b-0">
						<div className="font-bold text-[9px] text-base-content/40 uppercase tracking-[0.18em]">
							Selected mode
						</div>
						<div className="mt-1 flex items-center gap-2">
							<selectedMode.icon className="h-4 w-4 text-primary" />
							<span className="font-bold text-sm">{selectedMode.label}</span>
						</div>
						<div className="mt-1 text-[10px] text-base-content/45">{selectedMode.badge}</div>
					</div>
					<div className="border-base-300/70 border-b p-4 sm:border-r sm:border-b-0">
						<div className="font-bold text-[9px] text-base-content/40 uppercase tracking-[0.18em]">
							Preferred AI model
						</div>
						<div className="mt-1 flex flex-wrap items-center gap-2">
							<span className="font-bold text-sm">
								{selectedModel.family} {selectedModel.name}
							</span>
							{selectedModel.id === DEFAULT_AI_MODEL && (
								<span className="badge badge-primary badge-xs">Default</span>
							)}
						</div>
						<div className="mt-1 text-[10px] text-base-content/45">
							{aiEnabled ? "Used first when AI is eligible" : "Saved for Auto AI or AI mode"}
						</div>
					</div>
					<div className="p-4">
						<div className="font-bold text-[9px] text-base-content/40 uppercase tracking-[0.18em]">
							Safety path
						</div>
						<div className="mt-1 flex items-center gap-2">
							<ShieldCheck className="h-4 w-4 shrink-0 text-success" />
							<span className="font-bold text-sm">Automatic fallback</span>
						</div>
						<div className="mt-1 text-[10px] text-base-content/45 leading-relaxed">
							{safetyPath}
						</div>
					</div>
				</div>
			</div>

			<div className="overflow-hidden rounded-3xl border border-primary/20 bg-base-200/55 shadow-sm">
				<div className="flex flex-col gap-4 border-base-300/70 border-b bg-gradient-to-r from-primary/10 to-transparent px-6 py-5 sm:flex-row sm:items-center sm:justify-between">
					<div>
						<div className="flex items-center gap-2">
							<Gauge className="h-4.5 w-4.5 text-primary" />
							<h4 className="font-bold text-base-content">Upscaling Compatibility</h4>
						</div>
						<p className="mt-1 max-w-2xl text-base-content/55 text-xs leading-relaxed">
							Checks this server and fills in a safe starting mode and model for you.
						</p>
					</div>
					<button
						type="button"
						className="btn btn-primary rounded-full px-5 shadow-md shadow-primary/15"
						disabled={isReadOnly || isDetecting}
						onClick={runCompatibilityDetection}
					>
						{isDetecting ? (
							<span className="loading loading-spinner loading-sm" />
						) : (
							<RefreshCw className="h-4 w-4" />
						)}
						{isDetecting ? "Checking..." : "Auto Detect"}
					</button>
				</div>

				{detectionError && (
					<div className="m-4 rounded-xl border border-error/30 bg-error/10 p-3 text-error text-xs">
						{detectionError}
					</div>
				)}

				{compatibility ? (
					<div className="space-y-4 p-4 sm:p-5">
						<div className="grid gap-3 lg:grid-cols-2">
							<div className="rounded-2xl border border-primary/25 bg-primary/10 p-4">
								<div className="font-bold text-[10px] text-primary uppercase tracking-widest">
									Recommended
								</div>
								<div className="mt-1 font-black text-base-content text-lg">
									{recommendationLabel}
								</div>
								{recommendedModel?.id === DEFAULT_AI_MODEL && (
									<span className="badge badge-primary badge-sm mt-2">Recommended default</span>
								)}
								<div className="mt-2 text-[11px] text-base-content/55">
									Selected in the form. Use Save Changes below to apply it.
								</div>
							</div>

							<div
								className={`rounded-2xl border p-4 ${
									compatibility.standard.available
										? "border-success/30 bg-success/10"
										: "border-error/25 bg-error/5"
								}`}
							>
								<div className="flex items-center justify-between gap-3">
									<div className="font-bold text-base-content">Standard</div>
									<span
										className={`badge badge-sm ${
											compatibility.standard.available ? "badge-success" : "badge-error"
										}`}
									>
										{compatibility.standard.status}
									</span>
								</div>
								<div className="mt-2 break-words text-[11px] text-base-content/55 leading-relaxed">
									{compatibility.standard.details || "No scaler details were reported."}
								</div>
							</div>
						</div>

						<div className="rounded-2xl border border-base-300 bg-base-100/55 p-4">
							<div className="flex flex-wrap items-center justify-between gap-2">
								<div>
									<div className="font-bold text-base-content text-sm">AI model checks</div>
									<div className="mt-0.5 text-[10px] text-base-content/45">
										Compatibility only; real-time speed varies with resolution and GPU load.
									</div>
								</div>
								<span
									className={`badge ${compatibleModelCount > 0 ? "badge-success" : "badge-ghost"}`}
								>
									{compatibleModelCount} of {compatibility.models.length} compatible
								</span>
							</div>

							<div className="mt-3 grid gap-2 sm:grid-cols-2 xl:grid-cols-3">
								{compatibility.models.map((model) => (
									<div
										key={model.id}
										className={`rounded-xl border p-3 ${
											model.available
												? "border-success/25 bg-success/5"
												: "border-base-300 bg-base-200/55"
										}`}
									>
										<div className="flex items-center justify-between gap-3">
											<div className="font-bold text-xs">{model.label}</div>
											<span
												className={`badge badge-sm ${
													model.available ? "badge-success" : "badge-ghost"
												}`}
											>
												{model.status}
											</span>
										</div>
										{!model.available && model.details && (
											<div className="mt-1.5 break-words text-[10px] text-base-content/45 leading-relaxed">
												{model.details}
											</div>
										)}
									</div>
								))}
							</div>
						</div>

						{compatibility.notes?.map((note) => (
							<div key={note} className="flex gap-2 text-[10px] text-base-content/45">
								<Info className="mt-0.5 h-3 w-3 shrink-0" />
								<span>{note}</span>
							</div>
						))}
					</div>
				) : (
					<div className="flex flex-col gap-3 px-6 py-5 sm:flex-row sm:items-center sm:justify-between">
						<div>
							<div className="font-semibold text-base-content text-xs">Not checked yet</div>
							<div className="mt-1 text-[11px] text-base-content/45">
								Run Auto Detect after installing or changing GPU drivers.
							</div>
						</div>
						<div className="rounded-full border border-primary/20 bg-primary/10 px-3 py-1.5 text-[10px] text-primary">
							Safe starting point: Auto AI · FSRCNNX Fast
						</div>
					</div>
				)}
			</div>

			<div className="space-y-3">
				<div className="flex flex-wrap items-end justify-between gap-2 px-1">
					<div>
						<div className="font-bold text-[10px] text-primary uppercase tracking-[0.18em]">
							Step 1
						</div>
						<h4 className="mt-1 font-bold text-base-content text-lg">Choose how upscaling runs</h4>
					</div>
					<div className="text-[10px] text-base-content/45">
						Auto AI is recommended for most servers
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
										<span
											className={`badge badge-sm ${selected ? "badge-primary" : "badge-ghost"}`}
										>
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
			</div>

			{aiEnabled && (
				<div className="overflow-hidden rounded-3xl border border-secondary/20 bg-base-200/55 shadow-sm">
					<div className="flex flex-col gap-3 border-base-300/70 border-b bg-gradient-to-r from-secondary/10 to-transparent px-6 py-5 sm:flex-row sm:items-center sm:justify-between">
						<div>
							<div className="font-bold text-[9px] text-secondary uppercase tracking-[0.18em]">
								Step 2
							</div>
							<div className="flex items-center gap-2">
								<Sparkles className="h-4.5 w-4.5 text-secondary" />
								<h4 className="font-bold text-base-content">Choose the preferred AI model</h4>
							</div>
							<p className="mt-1 text-base-content/55 text-xs">
								Pick by content type and GPU load. Every model runs locally on this server.
							</p>
						</div>
						<div className="flex flex-wrap gap-2">
							<div className="rounded-full border border-primary/20 bg-primary/10 px-3 py-1.5 text-[10px] text-primary">
								Default · FSRCNNX Fast
							</div>
							<div className="flex items-center gap-2 rounded-full border border-success/20 bg-success/10 px-3 py-1.5 text-success text-xs">
								<ShieldCheck className="h-3.5 w-3.5" />
								Automatic fallback
							</div>
						</div>
					</div>

					<div className="space-y-4 p-4 sm:p-5">
						{AI_MODEL_GROUPS.map((group) => {
							const groupModels = AI_MODEL_OPTIONS.filter((option) =>
								(group.modelIds as readonly string[]).includes(option.id),
							);
							return (
								<section
									key={group.id}
									className="rounded-2xl border border-base-300/70 bg-base-100/40 p-3 sm:p-4"
								>
									<div className="flex flex-wrap items-start justify-between gap-2 px-1">
										<div>
											<h5 className="font-bold text-base-content text-sm">{group.label}</h5>
											<p className="mt-0.5 text-[10px] text-base-content/45">{group.description}</p>
										</div>
										<span className="badge badge-ghost badge-sm">
											{groupModels.length} {groupModels.length === 1 ? "model" : "models"}
										</span>
									</div>

									<div
										className={`mt-3 grid gap-3 ${
											group.id === "artcnn" ? "lg:grid-cols-2 2xl:grid-cols-3" : "lg:grid-cols-2"
										}`}
									>
										{groupModels.map((option) => {
											const selected = selectedModel.id === option.id;
											const Icon = option.icon;
											return (
												<button
													type="button"
													key={option.id}
													aria-pressed={selected}
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
								</section>
							);
						})}
					</div>

					<div className="flex flex-col gap-2 border-base-300/70 border-t bg-base-100/55 px-5 py-4 sm:flex-row sm:items-center sm:justify-between">
						<div className="text-xs">
							<span className="text-base-content/45">Preferred model</span>
							<span className="ml-2 font-bold text-base-content">
								{selectedModel.family} {selectedModel.name}
							</span>
						</div>
						<div className="text-[10px] text-base-content/45 leading-relaxed">
							If unavailable: {MODEL_FALLBACKS[selectedModel.id]}
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
						instead. ArtCNN and Anime4K are designed for animation and line art; FSRCNNX is the
						safer choice for general video.
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
