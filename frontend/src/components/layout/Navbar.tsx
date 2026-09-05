import { Menu } from "lucide-react";
import { UserMenu } from "../auth/UserMenu";

export function Navbar() {
	return (
		<header className="navbar sticky top-0 z-30 min-h-14 border-base-300 border-b bg-base-200/90 px-3 shadow-sm backdrop-blur-xl lg:px-6">
			<div className="navbar-start">
				<label
					htmlFor="sidebar-toggle"
					className="btn btn-square btn-ghost btn-sm transition-colors hover:bg-base-300/70 lg:hidden"
					aria-label="Open navigation"
				>
					<Menu className="h-5 w-5" />
				</label>
			</div>

			<div className="navbar-end">
				<UserMenu />
			</div>
		</header>
	);
}
