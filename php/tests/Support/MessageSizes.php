<?php

declare(strict_types=1);

namespace SimpleOrm\Tests\Support;

/** The sizes of the messages a session reported through its test hook (`Db::observeMessages()`), in order (ADR-0044). */
final class MessageSizes
{
    /** @var list<int> */
    private array $sizes = [];

    public function add(int $size): void
    {
        $this->sizes[] = $size;
    }

    /** @return list<int> */
    public function all(): array
    {
        return $this->sizes;
    }
}
