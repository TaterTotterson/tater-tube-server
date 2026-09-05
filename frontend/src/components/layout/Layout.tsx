import { Menu } from "lucide-react";
import { useState } from "react";
import { Outlet } from "react-router-dom";
import { Sidebar } from "./Sidebar";

export function Layout() {
	const [sidebarCollapsed, setSidebarCollapsed] = useState(
		() => window.localStorage.getItem("tater-sidebar-collapsed") === "true",
	);

	const toggleSidebar = () => {
		setSidebarCollapsed((current) => {
			const next = !current;
			window.localStorage.setItem("tater-sidebar-collapsed", String(next));
			return next;
		});
	};

	return (
		<div className="drawer lg:drawer-open">
			<input id="sidebar-toggle" type="checkbox" className="drawer-toggle" />

			<div className="drawer-content min-w-0 bg-transparent">
				<label
					htmlFor="sidebar-toggle"
					className="btn btn-square btn-primary fixed top-4 left-4 z-30 shadow-lg lg:hidden"
					aria-label="Open navigation"
				>
					<Menu className="h-5 w-5" />
				</label>

				{/* Page content */}
				<main className="min-w-0 px-4 pt-20 pb-6 md:px-6 lg:pt-6 xl:p-8">
					<Outlet />
				</main>
			</div>

			{/* Sidebar */}
			<div className="drawer-side">
				<label htmlFor="sidebar-toggle" aria-label="close sidebar" className="drawer-overlay" />
				<Sidebar collapsed={sidebarCollapsed} onToggleCollapsed={toggleSidebar} />
			</div>
		</div>
	);
}
