import React from 'react';
import { Link, Outlet } from 'react-router-dom';

export const Layout: React.FC = () => {
    return (
        <div className="min-h-screen flex flex-col font-sans">
            <header className="border-b border-gray-800 bg-background/50 backdrop-blur supports-[backdrop-filter]:bg-background/60">
                <div className="container flex h-14 max-w-screen-2xl items-center px-4">
                    <div className="mr-4 hidden md:flex">
                        <Link to="/" className="mr-6 flex items-center space-x-2">
                            <span className="hidden font-bold sm:inline-block">Sherwood</span>
                        </Link>
                        <nav className="flex items-center space-x-6 text-sm font-medium">
                            <Link to="/" className="transition-colors hover:text-foreground/80 text-foreground/60">
                                Dashboard
                            </Link>
                            <Link to="/config" className="transition-colors hover:text-foreground/80 text-foreground/60">
                                Configuration
                            </Link>
                        </nav>
                    </div>
                </div>
            </header>
            <main className="flex-1 container py-6 max-w-screen-2xl px-4">
                <Outlet />
            </main>
        </div>
    );
};
