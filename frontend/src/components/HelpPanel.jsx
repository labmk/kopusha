import React from 'react';
import * as Dialog from '@radix-ui/react-dialog';

// HelpPanel — what each control does, on demand.
//
// This is deliberately not a first-run tour. A coach-mark overlay has to
// know where things are, which couples it to a layout that moves; and it
// teaches at the one moment the user has least context, then never again.
// A reference one keystroke away can be opened when the question actually
// occurs, and cannot go stale by being mispositioned.
//
// Content is static on purpose. Anything derived from live state would be
// a second description of the interface, free to disagree with it.
const SECTIONS = [
  {
    title: 'Getting data in',
    rows: [
      ['+ Add', 'Browse the filesystem and load a log file. Several files load together and are queried as one table, even when their formats differ.'],
      ['Try the samples', 'Loads the sample logs shipped beside the binary — one per supported format — so there is something to query immediately.'],
      ['Checkbox beside a file', 'Include or exclude that file from the query without unloading it.'],
      ['Parser rules', 'Build a rule for a format kopusha does not recognise yet, from a sample you paste in.'],
    ],
  },
  {
    title: 'Narrowing the result',
    rows: [
      ['15m / 1h / 4h / 24h / 7d', 'Set the time range relative to the newest record.'],
      ['Drag across the histogram', 'Set the time range to what you dragged over.'],
      ['+ Filter', 'Add a field condition. Filters combine with AND.'],
      ['Search all fields', 'Free-text match across every column. * is a wildcard.'],
      ['Text', 'View or edit the whole query as pipeline-style text.'],
      ['Hide null', 'Leave null and empty fields out of an expanded row.'],
      ['Auto Apply', 'Re-run the query as filters change, instead of pressing Apply.'],
    ],
  },
  {
    title: 'Reading the result',
    rows: [
      ['▶ on a row', 'Expand the full record.'],
      ['Columns', 'Choose which columns are shown.'],
      ['Fields', 'What the data contains: which fields exist, how often they are populated, and their commonest values. Click a value to filter to it.'],
      ['Sort', 'Choose the timestamp field the table orders by.'],
    ],
  },
  {
    title: 'Getting data out',
    rows: [
      ['Export', 'Write the current result to NDJSON or Parquet.'],
      ['The address bar', 'Carries the whole view — filters, time range, sort, columns. Copy it to share the view. Files are not included; the recipient opens their own.'],
    ],
  },
  {
    title: 'Keyboard',
    rows: [
      ['j / ↓', 'Select the next row.'],
      ['k / ↑', 'Select the previous row.'],
      ['Esc', 'Close the selected row, or the panel you are in.'],
      ['Enter', 'Apply, in a filter or search box.'],
      ['?', 'Open this reference.'],
    ],
  },
];

export default function HelpPanel({ onClose }) {
  return (
    <Dialog.Root open onOpenChange={(o) => { if (!o) onClose(); }}>
      <Dialog.Portal>
        <Dialog.Overlay className="rdx-overlay" />
        <Dialog.Content className="rdx-content help-content" style={{ width: '640px' }}>
          <div className="modal-header">
            <Dialog.Title asChild><h3>What everything does</h3></Dialog.Title>
            <Dialog.Close asChild>
              <button className="btn btn-sm" aria-label="Close">&times;</button>
            </Dialog.Close>
          </div>
          <Dialog.Description className="sr-only">
            A reference for every control in kopusha, and the keyboard shortcuts.
          </Dialog.Description>
          <div className="modal-body help-body">
            {SECTIONS.map((section) => (
              <section key={section.title} className="help-section">
                <h4>{section.title}</h4>
                <dl>
                  {section.rows.map(([control, what]) => (
                    <div className="help-row" key={control}>
                      <dt>{control}</dt>
                      <dd>{what}</dd>
                    </div>
                  ))}
                </dl>
              </section>
            ))}
          </div>
          <div className="modal-footer help-foot">
            kopusha reads files you already have and serves this page to
            your own machine. Nothing is uploaded.
          </div>
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
