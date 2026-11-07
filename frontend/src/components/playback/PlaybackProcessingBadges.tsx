import type { ActiveStream } from "../../types/api";
import { PlaybackBufferBadge } from "./PlaybackBufferBadge";
import { PlaybackHDREnhancementBadge } from "./PlaybackHDREnhancementBadge";
import { PlaybackUpscalingBadge } from "./PlaybackUpscalingBadge";

function resolutionName(width?: number, height?: number) {
	const w = Number(width || 0);
	const h = Number(height || 0);
	if (w >= 3500 || h >= 2000) return "4K";
	if (w >= 2500 || h >= 1400) return "1440p";
	if (w >= 1800 || h >= 1000) return "1080p";
	if (w >= 1100 || h >= 650) return "720p";
	return w > 0 && h > 0 ? `${w}×${h}` : "";
}

function resolutionLabel(stream: ActiveStream) {
	const source = resolutionName(stream.source_width, stream.source_height);
	const output = resolutionName(stream.output_width, stream.output_height);
	const videoMode = stream.video_mode || (stream.transcoded ? "transcode" : "direct");
	if (
		videoMode === "transcode" &&
		source &&
		output &&
		(stream.source_width !== stream.output_width || stream.source_height !== stream.output_height)
	) {
		return `${source} → ${output}`;
	}
	return source || output;
}

function hardwareName(value?: string) {
	switch (String(value || "").toLowerCase()) {
		case "qsv":
			return "QUICK SYNC";
		case "vaapi":
			return "VAAPI";
		case "nvenc":
		case "cuda":
			return "NVENC";
		case "videotoolbox":
			return "VIDEOTOOLBOX";
		case "v4l2m2m":
			return "V4L2";
		default:
			return "TRANSCODE";
	}
}

function videoLabel(stream: ActiveStream) {
	const mode = stream.video_mode || (stream.transcoded ? "transcode" : "direct");
	if (mode === "direct" || mode === "copy") {
		return { label: "VIDEO · DIRECT", className: "badge-success" };
	}
	if (mode === "transcode" && stream.hardware_active) {
		return {
			label: `HW ${hardwareName(stream.hardware_acceleration)}`,
			className: "badge-success",
		};
	}
	if (mode === "transcode") {
		return { label: "SW TRANSCODE", className: "badge-warning" };
	}
	return { label: `VIDEO · ${mode.toUpperCase()}`, className: "badge-warning" };
}

function codecLabel(value?: string) {
	const codec = String(value || "")
		.trim()
		.toLowerCase();
	if (!codec) return "";
	const names: Record<string, string> = {
		aac: "AAC",
		ac3: "AC-3",
		eac3: "E-AC-3",
		dts: "DTS",
		dts_hd: "DTS-HD",
		truehd: "TRUEHD",
		opus: "OPUS",
		flac: "FLAC",
	};
	return names[codec] || codec.replaceAll("_", "-").toUpperCase();
}

function channelLabel(channels?: number) {
	switch (channels) {
		case 1:
			return "MONO";
		case 2:
			return "STEREO";
		case 6:
			return "5.1";
		case 8:
			return "7.1";
		default:
			return channels && channels > 0 ? `${channels} CH` : "";
	}
}

function audioLabel(stream: ActiveStream) {
	const mode = stream.audio_mode || (stream.transcoded ? "transcode" : "direct");
	const format = [codecLabel(stream.audio_codec), channelLabel(stream.audio_channels)]
		.filter(Boolean)
		.join(" ");
	const suffix = format ? ` · ${format}` : "";
	if (mode === "transcode") return `AUDIO · TRANSCODE${suffix}`;
	if (mode === "bitstream" || mode === "passthrough") return `AUDIO · BITSTREAM${suffix}`;
	if (mode === "none") return "AUDIO · NONE";
	return `AUDIO · DIRECT${suffix}`;
}

export function PlaybackProcessingBadges({
	stream,
	compact = false,
	includeBuffer = false,
}: {
	stream: ActiveStream;
	compact?: boolean;
	includeBuffer?: boolean;
}) {
	const resolution = resolutionLabel(stream);
	const video = videoLabel(stream);
	const sizeClass = compact ? "badge-sm text-[10px]" : "";
	return (
		<>
			{resolution && (
				<span className={`badge badge-primary badge-outline font-semibold ${sizeClass}`}>
					{resolution}
				</span>
			)}
			<PlaybackUpscalingBadge stream={stream} compact={compact} />
			<PlaybackHDREnhancementBadge stream={stream} compact={compact} />
			<span className={`badge ${video.className} font-semibold ${sizeClass}`}>{video.label}</span>
			<span className={`badge badge-info badge-outline font-semibold ${sizeClass}`}>
				{audioLabel(stream)}
			</span>
			{includeBuffer && <PlaybackBufferBadge stream={stream} compact={compact} />}
		</>
	);
}
