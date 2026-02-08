import axios from 'axios';

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

const api = axios.create({
    baseURL: API_URL,
});

export interface StatusResponse {
    status: string;
    version: string;
    mode: string;
}

export const getStatus = async (): Promise<StatusResponse> => {
    const response = await api.get('/status');
    return response.data;
};

export const startEngine = async () => {
    return api.post('/api/engine/start');
};

export const stopEngine = async () => {
    return api.post('/api/engine/stop');
};
