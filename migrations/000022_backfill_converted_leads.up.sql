BEGIN;

-- Bring the lead stage into line with the deals that already exist.
--
-- Two definitions of "converted" had grown apart. The stage pill and the
-- Converted tab's count read leads.status, while the deal link, the dialog's
-- Converted banner and the delete warning all read "does a deal point at this
-- lead?". Only the Convert action set the status, so a deal created from the
-- board satisfied the second and not the first: the lead sat at New while the
-- rest of the screen called it converted, and the tab's count disagreed with
-- the rows that looked converted. deals.create/update now set the status (see
-- markLeadConverted), which stops new drift; this clears what had accumulated.
--
-- 'closed' and 'not interested' are left exactly as they are. They are
-- deliberate outcomes somebody chose, and a linked deal is not enough to
-- overrule a human decision that the lead went nowhere — writing "Converted"
-- over them would invent a result. They keep their stage and their deal link,
-- and they are the one case where the two readings still differ on purpose.
UPDATE leads l
   SET status = 'converted',
       next_follow_up_date = NULL,
       updated_at = now()
 WHERE l.deleted_at IS NULL
   AND l.status NOT IN ('converted', 'closed', 'not interested')
   AND EXISTS (
         SELECT 1 FROM deals d
          WHERE d.lead_id = l.id AND d.deleted_at IS NULL
       );

COMMIT;
