/**
 * SelectionToolbar - Toolbar for selected nodes (delete, clear)
 * Extracted from Browser.tsx for reusability
 */

import { Trash2 } from "lucide-react";

interface SelectionToolbarProps {
  selectedCount: number;
  onDelete: () => void;
  onClear: () => void;
  deleting?: boolean;
}

export function SelectionToolbar({
  selectedCount,
  onDelete,
  onClear,
  deleting = false,
}: SelectionToolbarProps) {
  if (selectedCount === 0) return null;

  return (
    <div className="flex items-center gap-2 p-2 bg-norse-shadow border-b border-norse-rune">
      <span className="text-sm text-norse-silver">
        {selectedCount} selected
      </span>
      <button
        type="button"
        onClick={onDelete}
        disabled={deleting}
        className="flex items-center gap-1 px-3 py-1 text-sm bg-red-500/20 hover:bg-red-500/30 text-red-400 rounded disabled:opacity-50"
      >
        <Trash2 className="w-4 h-4" />
        Delete
      </button>
      <button
        type="button"
        onClick={onClear}
        className="px-3 py-1 text-sm text-norse-silver hover:text-white"
      >
        Clear
      </button>
    </div>
  );
}

