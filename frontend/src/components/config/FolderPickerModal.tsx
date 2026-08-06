import { ChevronUp, Folder, FolderCheck, HardDrive, X } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { useSystemBrowse } from "../../hooks/useApi";

interface FolderPickerModalProps {
	open: boolean;
	initialPath?: string;
	onClose: () => void;
	onSelect: (path: string) => void;
}

export function FolderPickerModal({
	open,
	initialPath = "",
	onClose,
	onSelect,
}: FolderPickerModalProps) {
	const [path, setPath] = useState(initialPath || "/");
	const [manualPath, setManualPath] = useState(initialPath || "/");
	const browser = useSystemBrowse(path);

	useEffect(() => {
		if (!open) return;
		const next = initialPath.trim() || "/";
		setPath(next);
		setManualPath(next);
	}, [open, initialPath]);

	const folders = useMemo(
		() =>
			(browser.data?.files ?? [])
				.filter((entry) => entry.is_dir)
				.sort((left, right) => left.name.localeCompare(right.name)),
		[browser.data?.files],
	);

	if (!open) return null;

	const navigate = (nextPath: string) => {
		setPath(nextPath);
		setManualPath(nextPath);
	};

	return (
		<div className="fixed inset-0 z-[80] flex items-center justify-center bg-black/65 p-4 backdrop-blur-sm">
			<div className="flex max-h-[85vh] w-full max-w-3xl flex-col overflow-hidden rounded-2xl border border-base-300 bg-base-100 shadow-2xl">
				<div className="flex items-start justify-between gap-4 border-base-300 border-b p-5">
					<div>
						<div className="flex items-center gap-2 font-bold text-lg">
							<HardDrive className="h-5 w-5 text-primary" />
							Choose a server folder
						</div>
						<p className="mt-1 text-base-content/55 text-sm">
							This browser shows folders visible to Tater Tube Server. Docker folders must be
							mounted into the container first.
						</p>
					</div>
					<button type="button" className="btn btn-ghost btn-sm btn-square" onClick={onClose}>
						<X className="h-5 w-5" />
					</button>
				</div>

				<div className="space-y-3 border-base-300 border-b bg-base-200/50 p-4">
					<div className="flex gap-2">
						<input
							type="text"
							className="input input-bordered w-full bg-base-100 font-mono text-sm"
							value={manualPath}
							onChange={(event) => setManualPath(event.target.value)}
							onKeyDown={(event) => {
								if (event.key === "Enter" && manualPath.trim()) navigate(manualPath.trim());
							}}
						/>
						<button
							type="button"
							className="btn btn-outline"
							disabled={!manualPath.trim() || browser.isFetching}
							onClick={() => navigate(manualPath.trim())}
						>
							Go
						</button>
					</div>
					<div className="flex min-w-0 items-center gap-2">
						<button
							type="button"
							className="btn btn-ghost btn-sm"
							disabled={!browser.data?.parent_path || browser.data.parent_path === path}
							onClick={() => browser.data?.parent_path && navigate(browser.data.parent_path)}
						>
							<ChevronUp className="h-4 w-4" />
							Parent
						</button>
						<div className="min-w-0 truncate font-mono text-base-content/55 text-xs">
							{browser.data?.current_path || path}
						</div>
					</div>
				</div>

				<div className="custom-scrollbar min-h-56 flex-1 overflow-y-auto p-3">
					{browser.isLoading && (
						<div className="flex h-48 items-center justify-center">
							<span className="loading loading-spinner loading-lg text-primary" />
						</div>
					)}
					{browser.error && (
						<div className="alert alert-error m-2">
							<span>
								{browser.error instanceof Error ? browser.error.message : "Unable to open folder"}
							</span>
						</div>
					)}
					{!browser.isLoading && !browser.error && folders.length === 0 && (
						<div className="flex h-48 items-center justify-center text-base-content/45 text-sm">
							No subfolders are visible here.
						</div>
					)}
					<div className="grid gap-1 sm:grid-cols-2">
						{folders.map((folder) => (
							<button
								key={folder.path}
								type="button"
								className="flex min-w-0 items-center gap-3 rounded-xl border border-transparent px-3 py-3 text-left hover:border-primary/30 hover:bg-primary/10"
								onClick={() => navigate(folder.path)}
							>
								<Folder className="h-5 w-5 shrink-0 text-primary/75" />
								<span className="truncate font-medium text-sm">{folder.name}</span>
							</button>
						))}
					</div>
				</div>

				<div className="flex flex-col-reverse gap-3 border-base-300 border-t p-4 sm:flex-row sm:items-center sm:justify-between">
					<div className="truncate font-mono text-base-content/55 text-xs">
						{browser.data?.current_path || path}
					</div>
					<div className="flex justify-end gap-2">
						<button type="button" className="btn btn-ghost" onClick={onClose}>
							Cancel
						</button>
						<button
							type="button"
							className="btn btn-primary"
							disabled={!browser.data?.current_path}
							onClick={() => browser.data?.current_path && onSelect(browser.data.current_path)}
						>
							<FolderCheck className="h-4 w-4" />
							Use This Folder
						</button>
					</div>
				</div>
			</div>
		</div>
	);
}
