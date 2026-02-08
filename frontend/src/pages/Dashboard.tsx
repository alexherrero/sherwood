import React, { useState } from 'react';
import { useQuery, useMutation } from '@tanstack/react-query';
import { getStatus, startEngine, stopEngine } from '../services/api';

export const Dashboard: React.FC = () => {
    const { data: status, refetch } = useQuery({
        queryKey: ['status'],
        queryFn: getStatus,
        refetchInterval: 5000
    });

    const [log, setLog] = useState<string[]>([]);

    const startMutation = useMutation({
        mutationFn: startEngine,
        onSuccess: () => {
            setLog(prev => [`[${new Date().toISOString()}] Engine Start Requested`, ...prev]);
            refetch();
        }
    });

    const stopMutation = useMutation({
        mutationFn: stopEngine,
        onSuccess: () => {
            setLog(prev => [`[${new Date().toISOString()}] Engine Stop Requested`, ...prev]);
            refetch();
        }
    });

    return (
        <div className="space-y-6">
            <div className="grid gap-4 md:grid-cols-2 lg:grid-cols-4">
                <div className="rounded-xl border bg-card text-card-foreground shadow p-6">
                    <div className="flex flex-row items-center justify-between space-y-0 pb-2">
                        <h3 className="tracking-tight text-sm font-medium">System Status</h3>
                    </div>
                    <div className="text-2xl font-bold">{status?.status || 'Offline'}</div>
                    <p className="text-xs text-muted-foreground">Version: {status?.version}</p>
                </div>
            </div>

            <div className="flex space-x-4">
                <button
                    onClick={() => startMutation.mutate()}
                    className="inline-flex items-center justify-center rounded-md text-sm font-medium ring-offset-background transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 h-10 px-4 py-2 bg-primary text-primary-foreground hover:bg-primary/90">
                    Start Engine
                </button>
                <button
                    onClick={() => stopMutation.mutate()}
                    className="inline-flex items-center justify-center rounded-md text-sm font-medium ring-offset-background transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:pointer-events-none disabled:opacity-50 h-10 px-4 py-2 bg-destructive text-destructive-foreground hover:bg-destructive/90 bg-red-600 text-white">
                    Stop Engine
                </button>
            </div>

            <div className="rounded-xl border bg-card text-card-foreground shadow">
                <div className="flex flex-col space-y-1.5 p-6">
                    <h3 className="font-semibold leading-none tracking-tight">Activity Log</h3>
                </div>
                <div className="p-6 pt-0">
                    <div className="bg-black/10 rounded-md p-4 h-64 overflow-y-auto font-mono text-xs">
                        {log.map((entry, i) => (
                            <div key={i}>{entry}</div>
                        ))}
                        {log.length === 0 && <div className="text-muted-foreground">No activity</div>}
                    </div>
                </div>
            </div>
        </div>
    );
};
