'use client';

import React, { useState, useEffect, useMemo, useRef } from 'react';
import { 
  Play, Pause, Download, RefreshCw, Search, Volume2, 
  FileText, Clock, MapPin, Database, CheckCircle2, 
  Calendar, Layers, Filter, Radio, ChevronRight, ExternalLink,
  ShieldCheck, ShieldAlert, LogIn
} from 'lucide-react';
import { getLocalIndexAsync, saveToLocalIndexAsync, IndexedRecording } from '@/lib/storage';

interface RecordingItem {
  id: string;
  device_id: string;
  title: string;
  recorded_at: string;
  duration: string;
  duration_ms: number;
  location?: string;
  has_transcript: boolean;
}

interface TranscriptSegment {
  speaker: string;
  start_time: string;
  start_ms: number;
  end_time: string;
  end_ms: number;
  text: string;
}

interface TranscriptData {
  recording_id: string;
  segments: TranscriptSegment[];
  full_text: string;
}

interface AuthStatus {
  authenticated: boolean;
  auth_user?: number;
  saved_at?: string;
  has_sapisid?: boolean;
  test_ok?: boolean;
  test_error?: string;
}

export default function RecorderStudio() {
  const [apiBase, setApiBase] = useState('http://localhost:8080');
  const [authStatus, setAuthStatus] = useState<AuthStatus | null>(null);
  const [refreshingAuth, setRefreshingAuth] = useState(false);
  const [recordings, setRecordings] = useState<RecordingItem[]>([]);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState<string | null>(null);

  // Filters & query
  const [searchQuery, setSearchQuery] = useState('');
  const [searchTranscripts, setSearchTranscripts] = useState(true);
  const [oldestFirst, setOldestFirst] = useState(false);
  const [fetchAll, setFetchAll] = useState(true); // default to true to capture the entire list

  // Selected & active playback
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [transcript, setTranscript] = useState<TranscriptData | null>(null);
  const [loadingTranscript, setLoadingTranscript] = useState(false);
  
  // Audio playback state
  const audioRef = useRef<HTMLAudioElement | null>(null);
  const [isPlaying, setIsPlaying] = useState(false);
  const [currentTime, setCurrentTime] = useState(0);
  const [duration, setDuration] = useState(0);

  // Local index cache for instant client-side full-text search
  const [localIndex, setLocalIndex] = useState<Record<string, IndexedRecording>>({});
  const [isIndexing, setIsIndexing] = useState(false);
  const [indexProgress, setIndexProgress] = useState({ current: 0, total: 0 });

  useEffect(() => {
    // Load from local IndexedDB storage
    getLocalIndexAsync().then(data => {
      setLocalIndex(data);
    });

    const initConnection = async () => {
      let activeBase = apiBase;
      for (const port of [8080, 8081, 8082]) {
        try {
          const res = await fetch(`http://localhost:${port}/api/v1/health`);
          if (res.ok) {
            activeBase = `http://localhost:${port}`;
            setApiBase(activeBase);
            break;
          }
        } catch {}
      }
      checkAuth(activeBase);
    };

    initConnection();
  }, []);

  useEffect(() => {
    fetchRecordings();
    checkAuth(apiBase);
  }, [oldestFirst, fetchAll, apiBase]);

  const checkAuth = async (base = apiBase) => {
    try {
      const res = await fetch(`${base}/api/v1/auth/status`);
      if (res.ok) {
        const data: AuthStatus = await res.json();
        setAuthStatus(data);
        return data;
      }
    } catch {}
    return null;
  };

  const handleRefreshAuth = async () => {
    setRefreshingAuth(true);
    try {
      const res = await fetch(`${apiBase}/api/v1/auth/refresh`, { method: 'POST' });
      if (res.ok) {
        const data: AuthStatus = await res.json();
        setAuthStatus(data);
        await fetchRecordings();
      } else {
        const err = await res.json();
        throw new Error(err.error || 'Refresh failed');
      }
    } catch (e: any) {
      setError(e.message || 'Failed to refresh session');
    } finally {
      setRefreshingAuth(false);
    }
  };

  const handleBrowserLogin = async () => {
    try {
      await fetch(`${apiBase}/api/v1/auth/login`, { method: 'POST' });
    } catch {}
  };

  const fetchRecordings = async () => {
    setLoading(true);
    setError(null);
    try {
      const url = `${apiBase}/api/v1/recordings?all=${fetchAll}&oldest=${oldestFirst}`;
      const res = await fetch(url);
      if (!res.ok) {
        throw new Error(`Server returned HTTP ${res.status}`);
      }
      const data = await res.json();
      const recs = data.recordings || [];
      setRecordings(recs);
      
      // Update local index with newly fetched records in background
      const itemsToSave: IndexedRecording[] = recs.map((r: RecordingItem) => ({
        id: r.id,
        deviceId: r.device_id,
        title: r.title,
        recordedAt: r.recorded_at,
        duration: r.duration,
        durationMs: r.duration_ms,
        location: r.location,
      }));
      await saveToLocalIndexAsync(itemsToSave);
      const updated = await getLocalIndexAsync();
      setLocalIndex(updated);
    } catch (e: any) {
      setError(e.message || 'Failed to connect to Recorder API');
    } finally {
      setLoading(false);
    }
  };

  const selectRecording = async (rec: RecordingItem) => {
    setSelectedId(rec.id);
    setTranscript(null);
    setLoadingTranscript(true);

    try {
      const res = await fetch(`${apiBase}/api/v1/recordings/${rec.id}/transcript`);
      if (res.ok) {
        const data: TranscriptData = await res.json();
        setTranscript(data);
        
        // Cache full text to IndexedDB
        await saveToLocalIndexAsync([{
          id: rec.id,
          deviceId: rec.device_id,
          title: rec.title,
          recordedAt: rec.recorded_at,
          duration: rec.duration,
          durationMs: rec.duration_ms,
          location: rec.location,
          transcriptText: data.full_text,
        }]);
        const updated = await getLocalIndexAsync();
        setLocalIndex(updated);
      }
    } catch (err) {
      console.error('Error fetching transcript:', err);
    } finally {
      setLoadingTranscript(false);
    }
  };

  // Bulk index all transcripts locally
  const indexAllTranscripts = async () => {
    if (recordings.length === 0) return;
    setIsIndexing(true);
    setIndexProgress({ current: 0, total: recordings.length });

    for (let i = 0; i < recordings.length; i++) {
      const rec = recordings[i];
      setIndexProgress({ current: i + 1, total: recordings.length });

      // Skip if already indexed
      if (localIndex[rec.id]?.transcriptText) continue;

      try {
        const res = await fetch(`${apiBase}/api/v1/recordings/${rec.id}/transcript`);
        if (res.ok) {
          const data: TranscriptData = await res.json();
          await saveToLocalIndexAsync([{
            id: rec.id,
            deviceId: rec.device_id,
            title: rec.title,
            recordedAt: rec.recorded_at,
            duration: rec.duration,
            durationMs: rec.duration_ms,
            location: rec.location,
            transcriptText: data.full_text,
          }]);
        }
      } catch (err) {
        console.error(`Failed to index ${rec.id}:`, err);
      }
      // Small throttle to prevent Google RPC rate limiting
      await new Promise(r => setTimeout(r, 60));
    }
    const updated = await getLocalIndexAsync();
    setLocalIndex(updated);
    setIsIndexing(false);
  };

  // Filtered list using local title AND transcript full-text index
  const filteredRecordings = useMemo(() => {
    const q = searchQuery.toLowerCase().trim();
    if (!q) return recordings;

    return recordings.filter(r => {
      const inTitle = r.title.toLowerCase().includes(q);
      const inLocation = (r.location || '').toLowerCase().includes(q);
      let inTranscript = false;

      if (searchTranscripts && localIndex[r.id]?.transcriptText) {
        inTranscript = localIndex[r.id].transcriptText!.toLowerCase().includes(q);
      }
      return inTitle || inLocation || inTranscript;
    });
  }, [recordings, searchQuery, searchTranscripts, localIndex]);

  const selectedRecording = recordings.find(r => r.id === selectedId);

  const togglePlay = () => {
    if (!audioRef.current) return;
    if (isPlaying) {
      audioRef.current.pause();
    } else {
      audioRef.current.play();
    }
    setIsPlaying(!isPlaying);
  };

  const seekTo = (seconds: number) => {
    if (audioRef.current) {
      audioRef.current.currentTime = seconds;
      if (!isPlaying) {
        audioRef.current.play();
        setIsPlaying(true);
      }
    }
  };

  const formatSec = (secs: number) => {
    const m = Math.floor(secs / 60);
    const s = Math.floor(secs % 60);
    return `${m}:${s.toString().padStart(2, '0')}`;
  };

  return (
    <div className="flex h-screen flex-col bg-slate-950 text-slate-100 antialiased overflow-hidden">
      {/* Top Navbar */}
      <header className="flex items-center justify-between border-b border-slate-800 bg-slate-900/70 px-6 py-3.5 backdrop-blur">
        <div className="flex items-center gap-3">
          <div className="flex h-9 w-9 items-center justify-center rounded-lg bg-red-600/20 text-red-500 font-bold border border-red-500/30">
            <Radio className="h-5 w-5 animate-pulse text-red-400" />
          </div>
          <div>
            <h1 className="text-base font-semibold tracking-tight text-white flex items-center gap-2">
              Google Recorder Studio
              <span className="rounded-full bg-emerald-950 border border-emerald-800 px-2 py-0.5 text-[10px] font-medium text-emerald-400">
                Go API Active
              </span>
            </h1>
            <p className="text-xs text-slate-400">Direct RPC & Zero-Allocation Audio Engine</p>
          </div>
        </div>

        {/* Server & Stats Controls */}
        <div className="flex items-center gap-3 text-xs">
          {/* Auth Status Badge */}
          {authStatus?.authenticated ? (
            <div className="flex items-center gap-1.5 rounded-md bg-emerald-950/80 px-2.5 py-1.5 border border-emerald-800/80 text-emerald-400">
              <ShieldCheck className="h-3.5 w-3.5" />
              <span>Google Account #{authStatus.auth_user ?? 0}</span>
              <button
                onClick={handleRefreshAuth}
                disabled={refreshingAuth}
                title="Refresh cookies from browser"
                className="ml-1 rounded p-0.5 hover:bg-emerald-900/60 transition disabled:opacity-50"
              >
                <RefreshCw className={`h-3 w-3 ${refreshingAuth ? 'animate-spin' : ''}`} />
              </button>
            </div>
          ) : (
            <div className="flex items-center gap-1.5 rounded-md bg-amber-950/80 px-2.5 py-1.5 border border-amber-800/80 text-amber-300">
              <ShieldAlert className="h-3.5 w-3.5 text-amber-400" />
              <span>Session Inactive</span>
              <button
                onClick={handleRefreshAuth}
                disabled={refreshingAuth}
                className="ml-1 rounded bg-amber-600/30 px-2 py-0.5 text-[11px] font-medium text-amber-200 hover:bg-amber-600/50 transition disabled:opacity-50"
              >
                {refreshingAuth ? 'Refreshing...' : 'Refresh'}
              </button>
              <button
                onClick={handleBrowserLogin}
                title="Open browser to sign in"
                className="rounded bg-slate-800 px-1.5 py-0.5 text-[11px] font-medium text-slate-300 hover:bg-slate-700 transition"
              >
                <LogIn className="h-3 w-3 inline mr-1" />
                Sign In
              </button>
            </div>
          )}

          <div className="flex items-center gap-2 rounded-md bg-slate-800/80 px-3 py-1.5 border border-slate-700">
            <span className="text-slate-400">Server:</span>
            <input 
              type="text" 
              value={apiBase} 
              onChange={e => setApiBase(e.target.value)} 
              className="bg-transparent text-slate-200 focus:outline-none w-36 font-mono text-xs"
            />
          </div>

          <button
            onClick={indexAllTranscripts}
            disabled={isIndexing}
            className="flex items-center gap-1.5 rounded-md bg-indigo-600/20 px-3 py-1.5 font-medium text-indigo-400 border border-indigo-500/30 hover:bg-indigo-600/30 transition disabled:opacity-50"
          >
            <Database className="h-3.5 w-3.5" />
            {isIndexing ? `Indexing ${indexProgress.current}/${indexProgress.total}...` : 'Index All Transcripts'}
          </button>

          <button
            onClick={fetchRecordings}
            className="flex items-center gap-1.5 rounded-md bg-slate-800 px-3 py-1.5 font-medium text-slate-200 border border-slate-700 hover:bg-slate-750 transition"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${loading ? 'animate-spin' : ''}`} />
            Refresh
          </button>
        </div>
      </header>

      {/* Main Workspace */}
      <div className="flex flex-1 overflow-hidden">
        {/* Left Column: Recording Explorer */}
        <aside className="flex w-96 flex-col border-r border-slate-800 bg-slate-900/30">
          {/* Search and Filters */}
          <div className="p-3.5 space-y-3 border-b border-slate-800">
            <div className="relative">
              <Search className="absolute left-3 top-2.5 h-4 w-4 text-slate-500" />
              <input
                type="text"
                placeholder="Search titles & transcripts..."
                value={searchQuery}
                onChange={e => setSearchQuery(e.target.value)}
                className="w-full rounded-lg bg-slate-800/70 py-2 pl-9 pr-4 text-xs text-white placeholder-slate-500 border border-slate-700/80 focus:border-indigo-500 focus:outline-none"
              />
            </div>

            <div className="flex items-center justify-between text-xs text-slate-400">
              <label className="flex items-center gap-1.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={searchTranscripts}
                  onChange={e => setSearchTranscripts(e.target.checked)}
                  className="rounded border-slate-700 text-indigo-600 focus:ring-0"
                />
                Search full transcripts
              </label>

              <div className="flex items-center gap-2">
                <button
                  onClick={() => setFetchAll(!fetchAll)}
                  className={`px-2 py-0.5 rounded border text-[11px] transition ${
                    fetchAll
                      ? 'border-emerald-500/50 bg-emerald-500/10 text-emerald-400 font-medium'
                      : 'border-slate-800 text-slate-500 hover:text-slate-300'
                  }`}
                  title="Toggle between fetching all historical recordings or recent 100"
                >
                  {fetchAll ? 'All History' : 'Recent 100'}
                </button>

                <button
                  onClick={() => setOldestFirst(!oldestFirst)}
                  className={`flex items-center gap-1 px-2 py-0.5 rounded border text-[11px] transition ${
                    oldestFirst 
                      ? 'border-indigo-500/50 bg-indigo-500/10 text-indigo-400 font-medium' 
                      : 'border-slate-800 text-slate-500 hover:text-slate-300'
                  }`}
                >
                  <Calendar className="h-3 w-3" />
                  {oldestFirst ? 'Oldest (2020)' : 'Newest'}
                </button>
              </div>
            </div>
          </div>

          {/* Recordings List */}
          <div className="flex-1 overflow-y-auto divide-y divide-slate-800/50">
            {error && (
              <div className="p-3.5 text-xs text-rose-400 bg-rose-950/30 border-b border-rose-900/40 space-y-2">
                <p className="font-medium">{error}</p>
                <div className="flex items-center gap-2 pt-1">
                  <button
                    onClick={handleRefreshAuth}
                    disabled={refreshingAuth}
                    className="flex items-center gap-1.5 rounded bg-rose-900/50 px-2.5 py-1 text-[11px] font-medium text-rose-200 hover:bg-rose-900/80 transition disabled:opacity-50"
                  >
                    <RefreshCw className={`h-3 w-3 ${refreshingAuth ? 'animate-spin' : ''}`} />
                    {refreshingAuth ? 'Refreshing...' : 'Auto-refresh from Browser'}
                  </button>
                  <button
                    onClick={handleBrowserLogin}
                    className="flex items-center gap-1.5 rounded bg-slate-800 px-2.5 py-1 text-[11px] font-medium text-slate-300 hover:bg-slate-700 transition"
                  >
                    <LogIn className="h-3 w-3" />
                    Open Google Recorder
                  </button>
                </div>
              </div>
            )}

            {loading && recordings.length === 0 ? (
              <div className="p-8 text-center text-xs text-slate-500 space-y-2">
                <RefreshCw className="h-5 w-5 animate-spin mx-auto text-slate-400" />
                <p>Connecting to Google Recorder...</p>
              </div>
            ) : filteredRecordings.length === 0 ? (
              <div className="p-8 text-center text-xs text-slate-500">
                No recordings match your filter.
              </div>
            ) : (
              filteredRecordings.map(rec => {
                const isSelected = rec.id === selectedId;
                const isIndexed = !!localIndex[rec.id]?.transcriptText;
                const dateStr = new Date(rec.recorded_at).toLocaleDateString(undefined, {
                  month: 'short',
                  day: 'numeric',
                  year: 'numeric'
                });

                return (
                  <div
                    key={rec.id}
                    onClick={() => selectRecording(rec)}
                    className={`p-3.5 cursor-pointer transition flex items-start justify-between group ${
                      isSelected
                        ? 'bg-indigo-600/15 border-l-2 border-indigo-500'
                        : 'hover:bg-slate-800/40'
                    }`}
                  >
                    <div className="space-y-1 pr-2 min-w-0">
                      <div className="flex items-center gap-1.5">
                        <h3 className={`text-xs font-medium truncate ${isSelected ? 'text-indigo-300' : 'text-slate-200'}`}>
                          {rec.title || 'Untitled Recording'}
                        </h3>
                        {isIndexed && (
                          <span title="Transcript indexed locally" className="inline-block w-1.5 h-1.5 rounded-full bg-emerald-400"></span>
                        )}
                      </div>

                      <div className="flex items-center gap-2 text-[11px] text-slate-400">
                        <span className="flex items-center gap-1">
                          <Calendar className="h-3 w-3 text-slate-500" />
                          {dateStr}
                        </span>
                        <span>•</span>
                        <span className="flex items-center gap-1">
                          <Clock className="h-3 w-3 text-slate-500" />
                          {rec.duration}
                        </span>
                      </div>

                      {rec.location && (
                        <p className="text-[11px] text-slate-500 flex items-center gap-1 truncate">
                          <MapPin className="h-3 w-3 flex-shrink-0" />
                          {rec.location}
                        </p>
                      )}
                    </div>
                    <ChevronRight className={`h-4 w-4 text-slate-600 transition group-hover:text-slate-400 ${isSelected ? 'text-indigo-400' : ''}`} />
                  </div>
                );
              })
            )}
          </div>

          <div className="p-3 border-t border-slate-800 text-[11px] text-slate-500 flex items-center justify-between">
            <span>Showing {filteredRecordings.length} of {recordings.length}</span>
            <div className="flex items-center gap-2">
              <span>Indexed: {Object.values(localIndex).filter(i => !!i.transcriptText).length}</span>
              <button
                onClick={() => {
                  const blob = new Blob([JSON.stringify(localIndex, null, 2)], { type: 'application/json' });
                  const url = URL.createObjectURL(blob);
                  const a = document.createElement('a');
                  a.href = url;
                  a.download = `google_recorder_index_${new Date().toISOString().slice(0, 10)}.json`;
                  a.click();
                  URL.revokeObjectURL(url);
                }}
                className="text-[10px] text-indigo-400 hover:text-indigo-300 underline"
                title="Export all indexed recordings and transcripts to a JSON file"
              >
                Export JSON
              </button>
            </div>
          </div>
        </aside>

        {/* Right Area: Detail, Audio Player & Interactive Transcript */}
        <main className="flex flex-1 flex-col overflow-hidden bg-slate-950">
          {selectedRecording ? (
            <div className="flex flex-1 flex-col overflow-hidden">
              {/* Recording Header & Download Actions */}
              <div className="border-b border-slate-800 p-6 bg-slate-900/20 flex items-start justify-between">
                <div>
                  <h2 className="text-lg font-semibold text-white">{selectedRecording.title}</h2>
                  <div className="flex items-center gap-3 text-xs text-slate-400 mt-1">
                    <span>Recorded {new Date(selectedRecording.recorded_at).toLocaleString()}</span>
                    <span>•</span>
                    <span>Duration: {selectedRecording.duration}</span>
                    {selectedRecording.location && (
                      <>
                        <span>•</span>
                        <span>{selectedRecording.location}</span>
                      </>
                    )}
                  </div>
                </div>

                <div className="flex items-center gap-2.5">
                  <a
                    href={`${apiBase}/api/v1/recordings/${selectedRecording.id}/transcript?format=text`}
                    download={`${selectedRecording.title}.txt`}
                    className="flex items-center gap-1.5 rounded-lg bg-slate-800 px-3 py-1.5 text-xs font-medium text-slate-200 border border-slate-700 hover:bg-slate-700 transition"
                  >
                    <FileText className="h-3.5 w-3.5 text-slate-400" />
                    Transcript (.txt)
                  </a>

                  <a
                    href={`${apiBase}/api/v1/recordings/${selectedRecording.id}/audio`}
                    download={`${selectedRecording.title}.m4a`}
                    className="flex items-center gap-1.5 rounded-lg bg-indigo-600 px-3.5 py-1.5 text-xs font-medium text-white shadow-sm hover:bg-indigo-500 transition"
                  >
                    <Download className="h-3.5 w-3.5" />
                    Download Audio (.m4a)
                  </a>
                </div>
              </div>

              {/* Streaming Audio Player Bar */}
              <div className="border-b border-slate-800 bg-slate-900/50 p-4">
                <audio
                  ref={audioRef}
                  src={`${apiBase}/api/v1/recordings/${selectedRecording.id}/audio`}
                  onTimeUpdate={() => {
                    if (audioRef.current) setCurrentTime(audioRef.current.currentTime);
                  }}
                  onLoadedMetadata={() => {
                    if (audioRef.current) setDuration(audioRef.current.duration);
                  }}
                  onPlay={() => setIsPlaying(true)}
                  onPause={() => setIsPlaying(false)}
                />

                <div className="flex items-center gap-4">
                  <button
                    onClick={togglePlay}
                    className="flex h-10 w-10 items-center justify-center rounded-full bg-indigo-600 text-white shadow-md hover:bg-indigo-500 transition"
                  >
                    {isPlaying ? <Pause className="h-5 w-5" /> : <Play className="h-5 w-5 ml-0.5" />}
                  </button>

                  <div className="flex-1 space-y-1">
                    <input
                      type="range"
                      min={0}
                      max={duration || selectedRecording.duration_ms / 1000 || 100}
                      value={currentTime}
                      onChange={e => seekTo(Number(e.target.value))}
                      className="w-full h-1.5 bg-slate-700 rounded-lg appearance-none cursor-pointer accent-indigo-500"
                    />
                    <div className="flex justify-between text-[11px] text-slate-400 font-mono">
                      <span>{formatSec(currentTime)}</span>
                      <span>{formatSec(duration || selectedRecording.duration_ms / 1000)}</span>
                    </div>
                  </div>

                  <Volume2 className="h-5 w-5 text-slate-400" />
                </div>
              </div>

              {/* Transcript Scroll Area with Click-to-Seek */}
              <div className="flex-1 overflow-y-auto p-6 space-y-4">
                <div className="flex items-center justify-between pb-2 border-b border-slate-800/80">
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-400">
                    Interactive Transcript
                  </h3>
                  <span className="text-xs text-slate-500">Click any timestamp to seek audio</span>
                </div>

                {loadingTranscript ? (
                  <div className="py-12 text-center text-xs text-slate-500 space-y-2">
                    <RefreshCw className="h-5 w-5 animate-spin mx-auto text-indigo-400" />
                    <p>Loading transcript from Google Recorder...</p>
                  </div>
                ) : transcript && transcript.segments.length > 0 ? (
                  <div className="space-y-4">
                    {transcript.segments.map((seg, idx) => (
                      <div 
                        key={idx} 
                        className="rounded-lg p-3 hover:bg-slate-900/60 transition group border border-transparent hover:border-slate-800"
                      >
                        <div className="flex items-center gap-2 mb-1.5">
                          <span className="text-xs font-semibold text-indigo-400">
                            {seg.speaker}
                          </span>
                          <button
                            onClick={() => seekTo(seg.start_ms / 1000)}
                            className="font-mono text-[11px] text-slate-500 hover:text-indigo-300 transition bg-slate-800/80 px-1.5 py-0.5 rounded"
                          >
                            {seg.start_time}
                          </button>
                        </div>
                        <p className="text-xs leading-relaxed text-slate-200">
                          {seg.text}
                        </p>
                      </div>
                    ))}
                  </div>
                ) : (
                  <div className="py-12 text-center text-xs text-slate-500">
                    No transcript available for this recording.
                  </div>
                )}
              </div>
            </div>
          ) : (
            <div className="flex flex-1 flex-col items-center justify-center p-8 text-center text-slate-500">
              <Radio className="h-12 w-12 text-slate-700 mb-3" />
              <h3 className="text-sm font-medium text-slate-300">No Recording Selected</h3>
              <p className="text-xs text-slate-500 max-w-sm mt-1">
                Select any recording on the left to view metadata, stream audio, inspect speaker turns, or download transcripts.
              </p>
            </div>
          )}
        </main>
      </div>
    </div>
  );
}
