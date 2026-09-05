import { Link } from 'react-router-dom'
import type { ScheduleItem } from '../lib/api'
import { relativeTime } from '../lib/format'
import { HoverInfo } from './HoverInfo'
import { PlayIcon } from './PosterCard'
import { Badge } from './ui'

/** A just-aired episode: the art plays it, the title opens the show. */
export function ReleasedCard({ item, tags }: { item: ScheduleItem; tags?: boolean }) {
  const watch = `/watch/${item.animeId}/${item.episode}`

  return (
    <div className="group/released">
      <HoverInfo
        anime={{
          id: item.animeId,
          title: item.title,
          romaji: item.romaji,
          english: item.english,
          format: item.format,
          color: item.colour,
          progress: item.progress,
          play: { to: watch, label: `Play ep ${item.episode}` },
        }}
      >
        <Link
          to={watch}
          className="block rounded-card focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent-400"
        >
          <div className="relative aspect-video overflow-hidden rounded-card bg-base-850 shadow-card ring-1 ring-white/[0.06] transition-[transform,box-shadow] duration-300 ease-out-quint group-hover/released:-translate-y-0.5 group-hover/released:shadow-lift">
            {item.cover && (
              <img
                src={item.cover}
                alt=""
                loading="lazy"
                className="size-full object-cover object-center transition-transform duration-300 group-hover/released:scale-105"
              />
            )}
            <div className="absolute inset-0 bg-gradient-to-t from-base-950/90 via-base-950/20 to-transparent" />

            {tags && (
              <div className="absolute top-1.5 right-1.5 flex gap-1">
                {item.watched ? (
                  <Badge tone="success">Watched</Badge>
                ) : item.behind > 0 ? (
                  <Badge tone="accent">{item.behind} behind</Badge>
                ) : item.onList ? (
                  <Badge>On list</Badge>
                ) : null}
              </div>
            )}

            <div className="absolute inset-x-0 bottom-0 p-2 text-[11px] text-base-300">
              <span className="font-medium text-white">Ep {item.episode}</span> · {relativeTime(item.airingAt)}
            </div>

            <div className="absolute inset-0 flex items-center justify-center opacity-0 transition-opacity duration-200 group-hover/released:opacity-100">
              <span className="grid size-10 place-items-center rounded-full bg-base-950/70 ring-1 ring-white/20 backdrop-blur-sm">
                <PlayIcon />
              </span>
            </div>
          </div>
        </Link>
      </HoverInfo>

      <Link to={`/anime/${item.animeId}`} className="mt-2 block">
        <p className="line-clamp-2 text-sm leading-snug text-base-200 transition-colors hover:text-white">
          {item.title}
        </p>
      </Link>
    </div>
  )
}
