import type { ReactNode } from "react";

export interface ConfigMiniTab {
	id: string;
	label: string;
	icon?: ReactNode;
	count?: number;
}

interface ConfigMiniTabsProps<T extends string> {
	tabs: Array<ConfigMiniTab & { id: T }>;
	activeTab: T;
	onChange: (tab: T) => void;
}

export function ConfigMiniTabs<T extends string>({
	tabs,
	activeTab,
	onChange,
}: ConfigMiniTabsProps<T>) {
	return (
		<div className="custom-scrollbar -mx-1 flex gap-2 overflow-x-auto px-1 pb-1">
			{tabs.map((tab) => {
				const isActive = tab.id === activeTab;
				return (
					<button
						key={tab.id}
						type="button"
						className={`btn btn-sm shrink-0 gap-2 rounded-lg border ${
							isActive
								? "btn-primary shadow-primary/20 shadow-md"
								: "border-base-300 bg-base-100/70 text-base-content/70 hover:border-primary/50 hover:bg-base-200"
						}`}
						onClick={() => onChange(tab.id)}
					>
						{tab.icon}
						{tab.label}
						{tab.count !== undefined && (
							<span className={`badge badge-xs ${isActive ? "badge-secondary" : "badge-ghost"}`}>
								{tab.count}
							</span>
						)}
					</button>
				);
			})}
		</div>
	);
}
