import { LogOut } from "lucide-react";
import { useAuth } from "../../hooks/useAuth";

interface UserMenuProps {
	compact?: boolean;
}

export function UserMenu({ compact = false }: UserMenuProps) {
	const { user, logout, isLoading, loginRequired } = useAuth();

	if (!user || loginRequired === false) {
		return null;
	}

	const handleLogout = async () => {
		try {
			await logout();
		} catch (error) {
			console.error("Logout failed:", error);
		}
	};

	return (
		<button
			type="button"
			onClick={handleLogout}
			disabled={isLoading}
			className={`btn btn-ghost gap-2 font-vcr text-base-content/80 hover:bg-error/10 hover:text-error disabled:cursor-not-allowed disabled:text-base-content/50 ${compact ? "btn-square" : "w-full justify-start"}`}
			title="Logout"
		>
			{isLoading ? (
				<span className="loading loading-spinner loading-sm" aria-hidden="true" />
			) : (
				<LogOut className="h-4 w-4" aria-hidden="true" />
			)}
			<span className={compact ? "sr-only" : "hidden sm:inline"}>
				{isLoading ? "LOGGING OUT" : "LOGOUT"}
			</span>
		</button>
	);
}
